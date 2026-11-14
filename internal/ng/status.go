package ng

import (
	"context"
	"log/slog"
	"maps"
	"slices"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/mocn/internal/assoc"
	"github.com/ellanetworks/mocn/internal/tracing"
)

// TS 38.413 clause 8.7.6
// TS 38.413 clause 9.2.6.10
// TS 38.300 clause 4.10.2.3
// TS 23.501 clause 5.21.2.2.1
// TS 23.502 clause 4.2.7.3
func (g *Gateway) amfStatusIndication(ctx context.Context, a *amfAssoc, log *slog.Logger, value []byte) {
	ind, err := ngap.ParseAMFStatusIndication(value)

	var unavailable []GUAMI

	backups := map[GUAMI]amfName{}

	if err == nil {
		for _, item := range ind.UnavailableGUAMIList {
			var v GUAMI
			if v, err = guamiFrom(item.GUAMI); err != nil {
				break
			}

			unavailable = append(unavailable, v)

			if b := backupNameFrom(item.BackupAMFName, item.ExtendedBackupAMFName); !b.empty() {
				backups[v] = b
			}
		}
	}

	if err != nil {
		tracing.SpanFailed(ctx, assoc.ErrTypeInvalid)
		log.WarnContext(ctx, "rejected AMF Status Indication from AMF", slog.Any("error", err))

		if codecError(err) {
			indicateParseError(ctx, a, log, ngap.ProcAMFStatusIndication, err)
		}

		return
	}

	var drained bool

	if !g.core.UpdateCore(ctx, a, func(c *amfClient) bool {
		for _, v := range unavailable {
			if !slices.Contains(c.Info.unavailable, v) {
				c.Info.unavailable = append(c.Info.unavailable, v)
			}
		}

		merged := maps.Clone(c.Info.backups)
		if merged == nil {
			merged = map[GUAMI]amfName{}
		}

		maps.Copy(merged, backups)
		c.Info.backups = merged

		drained = !(amfCandidate{served: c.Info.served, unavailable: c.Info.unavailable}).available()

		return true
	}) {
		return
	}

	log.InfoContext(ctx, "AMF Status Indication from AMF", slog.Int("guamis", len(unavailable)))
	g.relay.unbind(ctx, a, unavailable, drained)
}

// TS 38.413 clause 8.7.6
// TS 38.300 clause 4.10.2.3
func (p protocol) RadioNotifyLocked(ctx context.Context, n *ranAssoc, acked, want amfConfig) amfConfig {
	var newly []GUAMI

	for _, g := range want.unavailable {
		if !slices.Contains(acked.unavailable, g) {
			newly = append(newly, g)
		}
	}

	if len(newly) == 0 {
		return acked
	}

	list := make(ngap.UnavailableGUAMIList, 0, len(newly))

	for _, g := range newly {
		v, err := guamiTo(g)
		if err != nil {
			p.log.ErrorContext(ctx, "failed to encode an unavailable GUAMI", slog.Any("error", err))
			return acked
		}

		list = append(list, ngap.UnavailableGUAMIItem{GUAMI: v})
	}

	out, err := (&ngap.AMFStatusIndication{UnavailableGUAMIList: list}).Marshal()
	if err != nil {
		p.log.ErrorContext(ctx, "failed to marshal AMF Status Indication", slog.Any("error", err))
		return acked
	}

	if err := n.Send(ctx, out, assoc.NonUEStream); err != nil {
		n.Logger(p.log).WarnContext(ctx, "failed to send AMF Status Indication to radio", slog.Any("error", err))
		return acked
	}

	n.Logger(p.log).InfoContext(ctx, "AMF Status Indication sent to radio", slog.Int("guamis", len(newly)))

	acked.unavailable = append(slices.Clone(acked.unavailable), newly...)

	return acked
}
