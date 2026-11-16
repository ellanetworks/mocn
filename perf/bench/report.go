package bench

import (
	"fmt"
	"slices"
	"strings"
)

type key struct {
	path, dir string
	size      int
}

func Report(runs []Run) string {
	var w strings.Builder

	results := map[key][]Result{}

	var keys []key

	for _, r := range runs {
		for _, res := range r.Results {
			k := key{res.Path, res.Direction, res.Size}
			if _, ok := results[k]; !ok {
				keys = append(keys, k)
			}

			results[k] = append(results[k], res)
		}
	}

	fmt.Fprintf(&w, "Runs: %d\n\n", len(runs))
	fmt.Fprintln(&w, "| Path | Direction | Size (B) | Mpps median | Mpps min-max | Inner Gbps | Wire Gbps | Limit | "+
		"MOCN CPU avg % | MOCN busiest CPU % | MOCN cores | Slow path pkts | Dropped |")
	fmt.Fprintln(&w, "|---|---|---|---|---|---|---|---|---|---|---|---|---|")

	for _, k := range keys {
		rs := results[k]
		pps := pick(rs, func(r Result) float64 { return r.Pps })
		inner := pick(rs, func(r Result) float64 { return r.InnerGbps })
		wire := pick(rs, func(r Result) float64 { return r.WireGbps })
		cpu := pick(rs, func(r Result) float64 { return r.Confirmed.MOCN.AvgPct })
		busiest := pick(rs, func(r Result) float64 { return r.Confirmed.MOCN.MaxPct })
		cores := pick(rs, func(r Result) float64 { return r.Confirmed.MOCN.Cores })
		slow := pick(rs, func(r Result) float64 { return r.Confirmed.Metrics["ellamocn_user_plane_slow_path_packets_total"] })
		drop := pick(rs, func(r Result) float64 { return r.Confirmed.Metrics["ellamocn_user_plane_dropped_packets_total"] })

		var limits []string
		for _, r := range rs {
			if !slices.Contains(limits, r.Limit) {
				limits = append(limits, r.Limit)
			}
		}

		fmt.Fprintf(&w, "| %s | %s | %d | %.2f | %.2f-%.2f | %.2f | %.2f | %v | %.1f | %.1f | %.2f | %.0f | %.0f |\n",
			k.path, k.dir, k.size, median(pps)/1e6, slices.Min(pps)/1e6, slices.Max(pps)/1e6, median(inner),
			median(wire), limits, median(cpu), median(busiest), median(cores), slices.Max(slow), slices.Max(drop))
	}

	pings := map[string][]Ping{}

	var paths []string

	for _, r := range runs {
		for _, p := range r.Pings {
			if _, ok := pings[p.Path]; !ok {
				paths = append(paths, p.Path)
			}

			pings[p.Path] = append(pings[p.Path], p)
		}
	}

	if len(paths) == 0 {
		return w.String()
	}

	fmt.Fprintln(&w)
	fmt.Fprintln(&w, "| Path | Avg (ms) | Best (ms) | Worst (ms) | Mean deviation (ms) | p50 (ms) | p99 (ms) | Avg min-max (ms) | Lost |")
	fmt.Fprintln(&w, "|---|---|---|---|---|---|---|---|---|")

	for _, path := range paths {
		ps := pings[path]
		avg := pick(ps, func(p Ping) float64 { return p.Latency.AvgUs })
		best := pick(ps, func(p Ping) float64 { return p.Latency.MinUs })
		worst := pick(ps, func(p Ping) float64 { return p.Latency.MaxUs })
		mdev := pick(ps, func(p Ping) float64 { return p.Latency.MdevUs })
		p50 := pick(ps, func(p Ping) float64 { return p.Latency.P50Us })
		p99 := pick(ps, func(p Ping) float64 { return p.Latency.P99Us })
		lost := 0

		for _, p := range ps {
			lost += p.Latency.Sent - p.Latency.Received
		}

		fmt.Fprintf(&w, "| %s | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f | %.3f-%.3f | %d |\n", path, median(avg)/1e3,
			median(best)/1e3, median(worst)/1e3, median(mdev)/1e3, median(p50)/1e3, median(p99)/1e3,
			slices.Min(avg)/1e3, slices.Max(avg)/1e3, lost)
	}

	return w.String()
}

func pick[T any](xs []T, f func(T) float64) []float64 {
	out := make([]float64, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}

	return out
}

func median(v []float64) float64 {
	s := slices.Sorted(slices.Values(v))
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}

	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}
