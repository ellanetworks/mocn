package gw

import (
	"fmt"

	"github.com/ellanetworks/mocn/internal/db"
)

// TS 36.413 clause 9.2.3.8
// TS 38.413 clause 9.3.3.5
func EncodePLMN[T ~[3]byte](p db.PLMN) (T, error) {
	var out T

	if len(p.MCC) != 3 || (len(p.MNC) != 2 && len(p.MNC) != 3) || !isDigits(p.MCC+p.MNC) {
		return out, fmt.Errorf("invalid PLMN %q", p.String())
	}

	d := []byte(p.MCC + p.MNC)
	for i := range d {
		d[i] -= '0'
	}

	out[0] = d[1]<<4 | d[0]

	if len(p.MNC) == 2 {
		out[1] = 0xf0 | d[2]
		out[2] = d[4]<<4 | d[3]
	} else {
		out[1] = d[5]<<4 | d[2]
		out[2] = d[4]<<4 | d[3]
	}

	return out, nil
}

// TS 36.413 clause 9.2.3.8
// TS 38.413 clause 9.3.3.5
func DecodePLMN[T ~[3]byte](id T) (db.PLMN, error) {
	digit := func(v byte) (byte, error) {
		if v > 9 {
			return 0, fmt.Errorf("invalid PLMN identity %x", id[:])
		}

		return '0' + v, nil
	}

	var mcc, mnc []byte

	for _, v := range []byte{id[0] & 0x0f, id[0] >> 4, id[1] & 0x0f} {
		c, err := digit(v)
		if err != nil {
			return db.PLMN{}, err
		}

		mcc = append(mcc, c)
	}

	for _, v := range []byte{id[2] & 0x0f, id[2] >> 4} {
		c, err := digit(v)
		if err != nil {
			return db.PLMN{}, err
		}

		mnc = append(mnc, c)
	}

	if third := id[1] >> 4; third != 0x0f {
		c, err := digit(third)
		if err != nil {
			return db.PLMN{}, err
		}

		mnc = append(mnc, c)
	}

	return db.PLMN{MCC: string(mcc), MNC: string(mnc)}, nil
}

func isDigits(s string) bool {
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}
