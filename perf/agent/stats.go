package agent

import (
	"bufio"
	"bytes"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

type CPU struct {
	Name    string `json:"name"`
	User    uint64 `json:"user"`
	Nice    uint64 `json:"nice"`
	System  uint64 `json:"system"`
	Idle    uint64 `json:"idle"`
	IOWait  uint64 `json:"iowait"`
	IRQ     uint64 `json:"irq"`
	SoftIRQ uint64 `json:"softirq"`
	Steal   uint64 `json:"steal"`
}

func (c CPU) Total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

func (c CPU) Busy() uint64 {
	return c.Total() - c.Idle - c.IOWait
}

type Iface struct {
	RxPackets uint64 `json:"rx_packets"`
	RxDropped uint64 `json:"rx_dropped"`
	TxPackets uint64 `json:"tx_packets"`
	TxDropped uint64 `json:"tx_dropped"`
}

type Softnet struct {
	Processed uint64 `json:"processed"`
	Dropped   uint64 `json:"dropped"`
	Squeezed  uint64 `json:"squeezed"`
}

type Stats struct {
	At      time.Time        `json:"at"`
	CPUs    []CPU            `json:"cpus"`
	Ifaces  map[string]Iface `json:"ifaces"`
	Softnet Softnet          `json:"softnet"`
}

func ReadStats() (Stats, error) {
	s := Stats{At: time.Now(), Ifaces: map[string]Iface{}}

	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return s, err
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 9 || !strings.HasPrefix(f[0], "cpu") || f[0] == "cpu" {
			continue
		}

		v := make([]uint64, 8)
		for i := range v {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}

		s.CPUs = append(s.CPUs, CPU{
			Name: f[0], User: v[0], Nice: v[1], System: v[2], Idle: v[3], IOWait: v[4], IRQ: v[5], SoftIRQ: v[6],
			Steal: v[7],
		})
	}

	sys, err := os.OpenRoot("/sys/class/net")
	if err != nil {
		return s, err
	}
	defer func() { _ = sys.Close() }()

	links, err := fs.ReadDir(sys.FS(), ".")
	if err != nil {
		return s, err
	}

	for _, l := range links {
		name := l.Name()
		if name == "lo" {
			continue
		}

		s.Ifaces[name] = Iface{
			RxPackets: counter(sys, name, "rx_packets"), RxDropped: counter(sys, name, "rx_dropped"),
			TxPackets: counter(sys, name, "tx_packets"), TxDropped: counter(sys, name, "tx_dropped"),
		}
	}

	b, err = os.ReadFile("/proc/net/softnet_stat")
	if err != nil {
		return s, err
	}

	sc = bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}

		s.Softnet.Processed += hex(f[0])
		s.Softnet.Dropped += hex(f[1])
		s.Softnet.Squeezed += hex(f[2])
	}

	return s, nil
}

func counter(sys *os.Root, link, name string) uint64 {
	b, err := sys.ReadFile(link + "/statistics/" + name)
	if err != nil {
		return 0
	}

	v, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)

	return v
}

func hex(s string) uint64 {
	v, _ := strconv.ParseUint(s, 16, 64)
	return v
}
