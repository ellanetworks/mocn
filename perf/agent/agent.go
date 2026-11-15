package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/ellanetworks/mocn/perf/packet"
	"github.com/ellanetworks/mocn/perf/rx"
	"github.com/ellanetworks/mocn/perf/tx"
	"github.com/vishvananda/netlink"
)

var (
	errBusy      = errors.New("a counter is already attached")
	errNoCounter = errors.New("no counter attached")
	errNoNeigh   = errors.New("no neighbour entry for the next hop")
)

type TxRequest struct {
	Flows    []packet.Flow `json:"flows"`
	Size     int           `json:"size"`
	Ext      bool          `json:"ext"`
	Uplink   bool          `json:"uplink"`
	Rate     float64       `json:"rate"`
	Duration time.Duration `json:"duration"`
	CPUs     []int         `json:"cpus"`
	Batch    int           `json:"batch"`
}

type TxResponse struct {
	tx.Result

	Interface string `json:"interface"`
}

type RxRequest struct {
	Peer  netip.Addr `json:"peer"`
	Modes []rx.Mode  `json:"modes"`
}

type RxResponse struct {
	rx.Counts

	Interface string `json:"interface"`
}

type Agent struct {
	log     *slog.Logger
	mu      sync.Mutex
	counter *rx.Counter
	rxIface string
	txMu    sync.Mutex
}

func New(log *slog.Logger) *Agent {
	return &Agent{log: log}
}

func (a *Agent) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tx", a.tx)
	mux.HandleFunc("POST /rx/start", a.rxStart)
	mux.HandleFunc("POST /rx/stop", a.rxStop)
	mux.HandleFunc("GET /stats", a.stats)

	return mux
}

func (a *Agent) tx(w http.ResponseWriter, r *http.Request) {
	var req TxRequest
	if !decode(w, r, &req) {
		return
	}

	if len(req.Flows) == 0 {
		fail(w, http.StatusBadRequest, tx.ErrNoFlows)
		return
	}

	if !a.txMu.TryLock() {
		fail(w, http.StatusConflict, errBusy)
		return
	}
	defer a.txMu.Unlock()

	f := req.Flows[0]

	link, src, dst, err := nextHop(r.Context(), f.Src, f.Dst)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	res, err := tx.Run(r.Context(), tx.Config{
		Interface: link, SrcMAC: src, DstMAC: dst, Flows: req.Flows,
		Options: packet.Options{Inner: req.Size, Ext: req.Ext, Uplink: req.Uplink},
		Rate:    req.Rate, Duration: req.Duration, CPUs: req.CPUs, Batch: req.Batch,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	reply(w, TxResponse{Result: res, Interface: link})
}

func (a *Agent) rxStart(w http.ResponseWriter, r *http.Request) {
	var req RxRequest
	if !decode(w, r, &req) {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.counter != nil {
		fail(w, http.StatusConflict, errBusy)
		return
	}

	routes, err := netlink.RouteGet(req.Peer.AsSlice())
	if err != nil || len(routes) == 0 {
		fail(w, http.StatusInternalServerError, fmt.Errorf("route to %s: %w", req.Peer, err))
		return
	}

	l, err := netlink.LinkByIndex(routes[0].LinkIndex)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	c, err := rx.Attach(l.Attrs().Name, req.Modes)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	a.counter, a.rxIface = c, l.Attrs().Name
	a.log.InfoContext(r.Context(), "counter attached", slog.String("interface", a.rxIface))

	reply(w, RxResponse{Interface: a.rxIface})
}

func (a *Agent) rxStop(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.counter == nil {
		fail(w, http.StatusConflict, errNoCounter)
		return
	}

	counts, err := a.counter.Read()
	closeErr := a.counter.Close()
	a.counter = nil

	if err = errors.Join(err, closeErr); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	a.log.InfoContext(r.Context(), "counter detached", slog.String("interface", a.rxIface),
		slog.Uint64("packets", counts.Packets))

	reply(w, RxResponse{Counts: counts, Interface: a.rxIface})
}

func (a *Agent) stats(w http.ResponseWriter, _ *http.Request) {
	s, err := ReadStats()
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}

	reply(w, s)
}

func nextHop(ctx context.Context, src, dst netip.Addr) (string, net.HardwareAddr, net.HardwareAddr, error) {
	routes, err := netlink.RouteGetWithOptions(dst.AsSlice(), &netlink.RouteGetOptions{SrcAddr: src.AsSlice()})
	if err != nil || len(routes) == 0 {
		routes, err = netlink.RouteGet(dst.AsSlice())
	}

	if err != nil || len(routes) == 0 {
		return "", nil, nil, fmt.Errorf("route to %s: %w", dst, err)
	}

	rt := routes[0]

	l, err := netlink.LinkByIndex(rt.LinkIndex)
	if err != nil {
		return "", nil, nil, err
	}

	hop := dst.AsSlice()
	if rt.Gw != nil {
		hop = rt.Gw
	}

	for range 20 {
		neighs, err := netlink.NeighList(rt.LinkIndex, netlink.FAMILY_V4)
		if err != nil {
			return "", nil, nil, err
		}

		for _, n := range neighs {
			if n.IP.Equal(hop) && len(n.HardwareAddr) == 6 && n.State&(netlink.NUD_REACHABLE|netlink.NUD_STALE|
				netlink.NUD_DELAY|netlink.NUD_PROBE|netlink.NUD_PERMANENT) != 0 {
				return l.Attrs().Name, l.Attrs().HardwareAddr, n.HardwareAddr, nil
			}
		}

		poke(ctx, dst)
		time.Sleep(100 * time.Millisecond)
	}

	return "", nil, nil, fmt.Errorf("%w %s on %s", errNoNeigh, net.IP(hop), l.Attrs().Name)
}

func poke(ctx context.Context, dst netip.Addr) {
	var d net.Dialer

	c, err := d.DialContext(ctx, "udp", netip.AddrPortFrom(dst, 9).String())
	if err != nil {
		return
	}

	_, _ = c.Write([]byte{0})
	_ = c.Close()
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		fail(w, http.StatusBadRequest, err)
		return false
	}

	return true
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	http.Error(w, err.Error(), code)
}
