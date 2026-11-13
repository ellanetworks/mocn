package gw

import (
	"testing"

	"github.com/ellanetworks/mocn/internal/db"
)

type plmnIdentity [3]byte

func TestPLMNRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		plmn db.PLMN
		id   plmnIdentity
	}{
		{db.PLMN{MCC: "001", MNC: "01"}, plmnIdentity{0x00, 0xf1, 0x10}},
		{db.PLMN{MCC: "310", MNC: "410"}, plmnIdentity{0x13, 0x00, 0x14}},
	} {
		id, err := EncodePLMN[plmnIdentity](tc.plmn)
		if err != nil || id != tc.id {
			t.Fatalf("encode %s = %x %v, want %x", tc.plmn, id, err, tc.id)
		}

		p, err := DecodePLMN(tc.id)
		if err != nil || p != tc.plmn {
			t.Fatalf("decode %x = %s %v, want %s", tc.id, p, err, tc.plmn)
		}
	}

	if _, err := EncodePLMN[plmnIdentity](db.PLMN{MCC: "01", MNC: "01"}); err == nil {
		t.Fatal("encoded a 2-digit MCC")
	}

	if _, err := DecodePLMN(plmnIdentity{0xa0, 0xf1, 0x10}); err == nil {
		t.Fatal("decoded a non-digit")
	}
}
