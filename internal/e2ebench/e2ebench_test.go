package e2ebench

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func attachScenario(aggregated bool) benchScenario {
	return benchScenario{
		rat:        rat4G,
		aggregated: aggregated,
		cycle: func(w *benchWorker, s samples) error {
			start := time.Now()

			if err := w.enb.attach(&w.ue); err != nil {
				return fmt.Errorf("attach UE %d: %w", w.ue.id, err)
			}

			attached := time.Now()

			if err := w.enb.release(&w.ue); err != nil {
				return fmt.Errorf("release UE %d: %w", w.ue.id, err)
			}

			s.add("attach", attached.Sub(start))
			s.add("release", time.Since(attached))

			return nil
		},
	}
}

func registrationWithPDUScenario(aggregated bool) benchScenario {
	return benchScenario{
		rat:        rat5G,
		aggregated: aggregated,
		cycle: func(w *benchWorker, s samples) error {
			start := time.Now()

			if err := w.gnb.register(&w.ue); err != nil {
				return fmt.Errorf("register UE %d: %w", w.ue.id, err)
			}

			registered := time.Now()

			if err := w.gnb.establish(&w.ue); err != nil {
				return fmt.Errorf("establish the PDU session of UE %d: %w", w.ue.id, err)
			}

			established := time.Now()

			if err := w.gnb.release(&w.ue); err != nil {
				return fmt.Errorf("release UE %d: %w", w.ue.id, err)
			}

			s.add("reg", registered.Sub(start))
			s.add("pdu", established.Sub(registered))
			s.add("release", time.Since(established))

			return nil
		},
	}
}

func pagingScenario(r rat) benchScenario {
	return benchScenario{
		rat:    r,
		paging: true,
		cycle: func(w *benchWorker, s samples) error {
			start := time.Now()

			var err error
			if r == rat4G {
				err = w.m.mmes[w.ue.op].page(w.token)
			} else {
				err = w.m.amfs[w.ue.op].page(w.token)
			}

			if err != nil {
				return fmt.Errorf("send paging %#x: %w", w.token, err)
			}

			seen := make([]bool, *e2eBenchPagingRadios)

			for range seen {
				i, err := awaitOn(w.m, w.pages)
				if err != nil {
					return fmt.Errorf("await paging %#x at every radio: %w", w.token, err)
				}

				if seen[i] {
					return fmt.Errorf("paging %#x reached radio %d twice", w.token, i)
				}

				seen[i] = true
			}

			s.add("paging", time.Since(start))

			return nil
		},
	}
}

var e2eScenarios = []struct {
	name string
	sc   benchScenario
}{
	{"4g/Attach", attachScenario(false)},
	{"4g/AttachAggregated", attachScenario(true)},
	{"4g/Paging", pagingScenario(rat4G)},
	{"5g/RegistrationWithPDU", registrationWithPDUScenario(false)},
	{"5g/RegistrationWithPDUAggregated", registrationWithPDUScenario(true)},
	{"5g/Paging", pagingScenario(rat5G)},
}

func BenchmarkE2E(b *testing.B) {
	for _, tc := range e2eScenarios {
		b.Run(tc.name, func(b *testing.B) { runE2E(b, tc.sc) })
	}
}

func TestE2EBenchSmoke(t *testing.T) {
	for _, tc := range e2eScenarios {
		t.Run(tc.name, func(t *testing.T) {
			m := newBenchMOCN(t, tc.sc.rat, tc.sc.aggregated)
			m.uesPerRadio = 2

			if tc.sc.paging {
				m.radios(t, *e2eBenchPagingRadios)
			}

			workers := []*benchWorker{m.worker(t, tc.sc, 0), m.worker(t, tc.sc, 1), m.worker(t, tc.sc, 2)}

			var wg sync.WaitGroup

			errs := make([]error, len(workers))

			for i, w := range workers {
				wg.Go(func() {
					for range 2 {
						if errs[i] = tc.sc.cycle(w, samples{}); errs[i] != nil {
							return
						}
					}
				})
			}

			wg.Wait()

			for _, err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}

			m.mustVerifyIdle(t)
		})
	}
}
