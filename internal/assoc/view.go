package assoc

import (
	"slices"
	"strings"
)

type coreConfig[C any] struct {
	cfg C
	ok  bool
}

type configView[C any] struct {
	gen        uint64
	byOperator map[int64]coreConfig[C]
}

type pagingView[S, R any] struct {
	gen   uint64
	byTAI map[TAI][]*Radio[S, R]
}

func (g *Gateway[S, R, I, C]) radiosChangedLocked() {
	g.radioGen++
}

func (g *Gateway[S, R, I, C]) configChangedLocked() {
	g.configGen++
	g.radiosChangedLocked()

	for _, c := range g.cores {
		c.kick()
	}
}

func (g *Gateway[S, R, I, C]) configsLocked() map[int64]coreConfig[C] {
	if g.configs != nil && g.configs.gen == g.configGen {
		return g.configs.byOperator
	}

	set := make([]*Radio[S, R], 0, len(g.radios))

	for _, n := range g.radios {
		if n.Setup != nil {
			set = append(set, n)
		}
	}

	slices.SortFunc(set, func(a, b *Radio[S, R]) int { return strings.Compare(a.Ref(), b.Ref()) })

	setups := make([]S, len(set))
	for i, n := range set {
		setups[i] = *n.Setup
	}

	v := &configView[C]{gen: g.configGen, byOperator: make(map[int64]coreConfig[C], len(g.operators))}

	for _, o := range g.operators {
		cfg, ok := g.proto.CoreConfig(o.PLMNs, setups)
		v.byOperator[o.ID] = coreConfig[C]{cfg: cfg, ok: ok}
	}

	g.configs = v

	return v.byOperator
}

func (g *Gateway[S, R, I, C]) pagingLocked() map[TAI][]*Radio[S, R] {
	if g.paging != nil && g.paging.gen == g.radioGen {
		return g.paging.byTAI
	}

	v := &pagingView[S, R]{gen: g.radioGen, byTAI: map[TAI][]*Radio[S, R]{}}

	for _, n := range g.radios {
		if !n.Up || n.Setup == nil {
			continue
		}

		for _, tai := range (*n.Setup).TAIs() {
			if l := v.byTAI[tai]; len(l) == 0 || l[len(l)-1] != n {
				v.byTAI[tai] = append(v.byTAI[tai], n)
			}
		}
	}

	g.paging = v

	return v.byTAI
}
