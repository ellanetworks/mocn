package gtpu

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target bpfel,bpfeb -type tunnel -type tunnel_activity -type iface -type counter -type family -type iface_flag -type config_flag -type limits -type drop_reason -type pass_reason relay bpf/relay.c -- -nostdinc -Ibpf/include -O2 -Wall -Werror -g
