package rx

import (
	"errors"
	"fmt"
	"net"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
)

const (
	xdpDrop    = 1
	xdpPass    = 2
	minHeaders = 14 + 20 + 8 + 8
	ipv4LE     = 0x0008
	gtpuPortLE = 0x6808
	fragMaskLE = 0xff3f
	ipv4NoOpts = 0x45
	udpProto   = 17
	gpdu       = 0xff
)

type Mode string

const (
	Native  Mode = "native"
	Generic Mode = "generic"
)

type Counts struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
	Mode    Mode   `json:"mode"`
}

type counter struct {
	Packets uint64
	Bytes   uint64
}

type Counter struct {
	m    *ebpf.Map
	prog *ebpf.Program
	l    link.Link
	mode Mode
}

func Attach(iface string, modes []Mode) (*Counter, error) {
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}

	m, err := ebpf.NewMap(&ebpf.MapSpec{Type: ebpf.PerCPUArray, KeySize: 4, ValueSize: 16, MaxEntries: 1})
	if err != nil {
		return nil, fmt.Errorf("counter map: %w", err)
	}

	prog, err := ebpf.NewProgram(&ebpf.ProgramSpec{Type: ebpf.XDP, License: "GPL", Instructions: program(m.FD())})
	if err != nil {
		_ = m.Close()
		return nil, fmt.Errorf("counter program: %w", err)
	}

	var errs []error

	for _, mode := range modes {
		flags := link.XDPDriverMode
		if mode == Generic {
			flags = link.XDPGenericMode
		}

		l, err := link.AttachXDP(link.XDPOptions{Program: prog, Interface: ifi.Index, Flags: flags})
		if err == nil {
			return &Counter{m: m, prog: prog, l: l, mode: mode}, nil
		}

		errs = append(errs, fmt.Errorf("%s: %w", mode, err))
	}

	_ = prog.Close()
	_ = m.Close()

	return nil, fmt.Errorf("attach XDP to %s: %w", iface, errors.Join(errs...))
}

func program(fd int) asm.Instructions {
	return asm.Instructions{
		asm.LoadMem(asm.R2, asm.R1, 0, asm.Word),
		asm.LoadMem(asm.R3, asm.R1, 4, asm.Word),
		asm.Mov.Reg(asm.R4, asm.R2),
		asm.Add.Imm(asm.R4, minHeaders),
		asm.JGT.Reg(asm.R4, asm.R3, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 12, asm.Half),
		asm.JNE.Imm(asm.R5, ipv4LE, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 14, asm.Byte),
		asm.JNE.Imm(asm.R5, ipv4NoOpts, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 20, asm.Half),
		asm.And.Imm(asm.R5, fragMaskLE),
		asm.JNE.Imm(asm.R5, 0, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 23, asm.Byte),
		asm.JNE.Imm(asm.R5, udpProto, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 36, asm.Half),
		asm.JNE.Imm(asm.R5, gtpuPortLE, "pass"),
		asm.LoadMem(asm.R5, asm.R2, 43, asm.Byte),
		asm.JNE.Imm(asm.R5, gpdu, "pass"),
		asm.LoadMem(asm.R7, asm.R2, 16, asm.Half),
		asm.HostTo(asm.BE, asm.R7, asm.Half),
		asm.Add.Imm(asm.R7, 14),
		asm.StoreImm(asm.RFP, -4, 0, asm.Word),
		asm.LoadMapPtr(asm.R1, fd),
		asm.Mov.Reg(asm.R2, asm.RFP),
		asm.Add.Imm(asm.R2, -4),
		asm.FnMapLookupElem.Call(),
		asm.JEq.Imm(asm.R0, 0, "drop"),
		asm.LoadMem(asm.R1, asm.R0, 0, asm.DWord),
		asm.Add.Imm(asm.R1, 1),
		asm.StoreMem(asm.R0, 0, asm.R1, asm.DWord),
		asm.LoadMem(asm.R1, asm.R0, 8, asm.DWord),
		asm.Add.Reg(asm.R1, asm.R7),
		asm.StoreMem(asm.R0, 8, asm.R1, asm.DWord),
		asm.Mov.Imm(asm.R0, xdpDrop).WithSymbol("drop"),
		asm.Return(),
		asm.Mov.Imm(asm.R0, xdpPass).WithSymbol("pass"),
		asm.Return(),
	}
}

func (c *Counter) Read() (Counts, error) {
	var per []counter

	if err := c.m.Lookup(uint32(0), &per); err != nil {
		return Counts{}, err
	}

	out := Counts{Mode: c.mode}

	for _, p := range per {
		out.Packets += p.Packets
		out.Bytes += p.Bytes
	}

	return out, nil
}

func (c *Counter) Close() error {
	return errors.Join(c.l.Close(), c.prog.Close(), c.m.Close())
}
