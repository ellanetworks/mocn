package tnl

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/ellanetworks/core/ngap"
	"github.com/ellanetworks/core/per"
	"github.com/ellanetworks/core/per/ap"
	"github.com/ellanetworks/core/per/ap/apimpl"
)

var ErrUnknownExtension = errors.New("unknown extension in a structure that carries a transport layer address")

var leaves = []reflect.Type{
	reflect.TypeFor[ap.TransportLayerAddress](),
	reflect.TypeFor[ngap.TransferContainer](),
}

var carriers sync.Map

// TS 36.414 clause 5.1
// TS 38.414 clause 5.1
func Octets(a ap.TransportLayerAddress) []byte {
	if !a.Valid() {
		return nil
	}

	return a.Bytes()
}

// TS 36.414 clause 5.1
// TS 38.414 clause 5.1
func Address(addr netip.Addr) (ap.TransportLayerAddress, error) {
	return ap.TransportLayerAddressFrom(addr)
}

func Lossless(v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("tnl: %T is not a pointer to a struct", v)
	}

	return walk(rv.Elem())
}

func walk(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}

		return walk(v.Elem())
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := walk(v.Index(i)); err != nil {
				return err
			}
		}

		return nil
	case reflect.Struct:
		return walkStruct(v)
	default:
		return nil
	}
}

func walkStruct(v reflect.Value) error {
	t := v.Type()
	if !carries(t) || slices.Contains(leaves, t) {
		return nil
	}

	if err := local(v); err != nil {
		return err
	}

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		if unknownAlternative(f) && !v.Field(i).IsNil() {
			return fmt.Errorf("%w: %s", ErrUnknownExtension, t)
		}

		if err := walk(v.Field(i)); err != nil {
			return err
		}
	}

	return nil
}

func unknownAlternative(f reflect.StructField) bool {
	opts := strings.Split(f.Tag.Get("per"), ",")

	return f.Type.Kind() == reflect.Pointer && (slices.Contains(opts, "extalt") ||
		slices.Contains(opts, "extie") && slices.ContainsFunc(opts, func(o string) bool { return strings.HasPrefix(o, "choice:") }))
}

func local(v reflect.Value) error {
	t := v.Type()

	if f := v.FieldByName("Message"); f.IsValid() {
		if m, ok := f.Addr().Interface().(*apimpl.Message[ngap.ProtocolIEID]); ok {
			if len(m.UnknownIEs()) > 0 || apimpl.MessageAdditions(m).Present() {
				return fmt.Errorf("%w: %s", ErrUnknownExtension, t)
			}

			return nil
		}
	}

	if !slices.ContainsFunc(reflect.VisibleFields(t), func(f reflect.StructField) bool { return !f.IsExported() }) {
		return nil
	}

	got, ok := v.Addr().Interface().(per.Marshaler)
	if !ok {
		return fmt.Errorf("tnl: %s is not a PER marshaler", t)
	}

	clean := reflect.New(t)

	for i := range t.NumField() {
		if t.Field(i).IsExported() {
			clean.Elem().Field(i).Set(v.Field(i))
		}
	}

	want, ok := clean.Interface().(per.Marshaler)
	if !ok {
		return fmt.Errorf("tnl: %s is not a PER marshaler", t)
	}

	a, err := per.Marshal(got, per.Aligned)
	if err != nil {
		return err
	}

	b, err := per.Marshal(want, per.Aligned)
	if err != nil {
		return err
	}

	if !bytes.Equal(a, b) {
		return fmt.Errorf("%w: %s", ErrUnknownExtension, t)
	}

	return nil
}

func carries(t reflect.Type) bool {
	if c, ok := carriers.Load(t); ok {
		return c.(bool) //nolint:forcetypeassert
	}

	c := reaches(t, map[reflect.Type]bool{})
	carriers.Store(t, c)

	return c
}

func reaches(t reflect.Type, seen map[reflect.Type]bool) bool {
	if slices.Contains(leaves, t) {
		return true
	}

	if seen[t] {
		return false
	}

	seen[t] = true

	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return reaches(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() && reaches(f.Type, seen) {
				return true
			}
		}
	}

	return false
}
