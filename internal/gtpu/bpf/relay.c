#include <linux/types.h>
#include <linux/bpf.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

#define ETH_HLEN 14
#define ETH_P_IP 0x0800
#define ETH_P_IPV6 0x86DD
#define IPPROTO_UDP 17
#define IPV6_FRAGMENT 44
#define IPV4_DF 0x4000
#define GTPU_PORT 2152
#define GTP_GPDU 255
#define GTP_END_MARKER 254
#define GTP_TUNNEL_STATUS 253
#define GTP_FLAG_E 0x04
#define GTP_FLAG_S 0x02
#define GTP_FLAG_PN 0x01
#define GTP_HLEN 8
#define GTP_OPTIONAL_LEN 4
#define OUTER_TTL 64

enum family {
	FAMILY_IPV4 = 2,
	FAMILY_IPV6 = 10,
};

enum iface_flag {
	IFACE_L3 = 1,
	IFACE_XFRM = 2,
};

enum config_flag {
	CONFIG_POLICY_IPSEC = 1,
};

enum limits {
	MAX_LEGS = 64,
	MAX_EXT_HEADERS = 8,
	MAX_IFACES = 1024,
};

enum drop_reason {
	DROP_WRONG_LEG,
	DROP_WRONG_EGRESS,
	DROP_FIB_BLACKHOLE,
	DROP_FIB_UNREACHABLE,
	DROP_FIB_PROHIBIT,
	DROP_REWRITE_FAILED,
	DROP_REASONS,
};

enum pass_reason {
	PASS_NO_TUNNEL,
	PASS_NOT_LOCAL,
	PASS_IP_FRAGMENT,
	PASS_EXT_HEADER,
	PASS_ZERO_CHECKSUM,
	PASS_MERGED,
	PASS_FRAG_NEEDED,
	PASS_FWD_DISABLED,
	PASS_IPSEC_POLICY,
	PASS_FIB_OTHER,
	PASS_REASONS,
};

struct abi {
	enum family family;
	enum iface_flag iface_flag;
	enum config_flag config_flag;
	enum limits limits;
	enum drop_reason drop_reason;
	enum pass_reason pass_reason;
};

const struct abi abi_types __attribute__((used));

struct tunnel {
	__u8 local[16];
	__u8 src[16];
	__u8 dst[16];
	__be32 teid;
	__u32 path;
	__u32 out_ifindex;
	__be16 sport;
	__u8 family;
	__u8 leg;
	__u8 op;
	__u8 pad[3];
};

struct iface {
	__u64 legs;
	__u32 flags;
	__u32 pad;
};

struct counter {
	__u64 packets;
	__u64 bytes;
};

struct tunnel_activity {
	__u8 last[16];
	__u64 packets;
	__u64 bytes;
};

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, __u32);
	__type(value, struct tunnel);
	__uint(max_entries, 1);
} tunnels SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, __u32);
	__type(value, struct tunnel_activity);
	__uint(max_entries, 1);
} tunnel_stats SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__type(key, __u32);
	__type(value, struct iface);
	__uint(max_entries, MAX_IFACES);
} ifaces SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__type(key, __u32);
	__type(value, __u32);
	__uint(max_entries, 1);
} config SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__type(key, __u32);
	__type(value, struct counter);
	__uint(max_entries, 1);
} path_stats SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__type(key, __u32);
	__type(value, __u64);
	__uint(max_entries, MAX_LEGS *DROP_REASONS);
} drops SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
	__type(key, __u32);
	__type(value, __u64);
	__uint(max_entries, PASS_REASONS);
} passes SEC(".maps");

static __always_inline int pass(__u32 reason)
{
	__u64 *n = bpf_map_lookup_elem(&passes, &reason);

	if (n)
		*n += 1;

	return TCX_NEXT;
}

static __always_inline int drop(__u8 op, __u32 reason)
{
	__u32 key = (__u32)(op % MAX_LEGS) * DROP_REASONS + reason;
	__u64 *n = bpf_map_lookup_elem(&drops, &key);

	if (n)
		*n += 1;

	return TCX_DROP;
}

struct parsed {
	__u32 l3;
	__u32 l4;
	__u32 gtp;
	__u16 tot_len;
	__u16 check;
	__u8 family;
	__u8 tos;
	__be32 teid;
	__u8 daddr[16];
	__u8 saddr[16];
};

static __always_inline int same_header(const __u32 *a, const __u32 *b)
{
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2] && a[3] == b[3] &&
	       a[4] == b[4];
}

static __always_inline int network_offset(struct __sk_buff *skb, __u32 *off)
{
	__u32 net[5], at[5];

	if (bpf_skb_load_bytes_relative(skb, 0, net, sizeof(net),
					BPF_HDR_START_NET))
		return -1;

	if (!bpf_skb_load_bytes(skb, 0, at, sizeof(at)) &&
	    same_header(net, at)) {
		*off = 0;
		return 0;
	}

	if (!bpf_skb_load_bytes(skb, ETH_HLEN, at, sizeof(at)) &&
	    same_header(net, at)) {
		*off = ETH_HLEN;
		return 0;
	}

	return -1;
}

static __always_inline int parse_ipv4(struct __sk_buff *skb, struct parsed *p)
{
	__u8 hdr[20];

	if (bpf_skb_load_bytes(skb, p->l3, hdr, sizeof(hdr)))
		return -1;

	if ((hdr[0] >> 4) != 4 || hdr[9] != IPPROTO_UDP)
		return -1;

	__u32 ihl = (hdr[0] & 0x0f) * 4;

	if (ihl < 20)
		return -1;

	if (((hdr[6] & 0x3f) | hdr[7]) != 0)
		return 1;

	p->family = FAMILY_IPV4;
	p->tos = hdr[1];
	p->tot_len = ((__u16)hdr[2] << 8) | hdr[3];
	__builtin_memcpy(p->saddr, &hdr[12], 4);
	__builtin_memcpy(p->daddr, &hdr[16], 4);
	p->l4 = p->l3 + ihl;

	return 0;
}

static __always_inline int parse_ipv6(struct __sk_buff *skb, struct parsed *p)
{
	__u8 hdr[40];

	if (bpf_skb_load_bytes(skb, p->l3, hdr, sizeof(hdr)))
		return -1;

	if ((hdr[0] >> 4) != 6)
		return -1;

	if (hdr[6] == IPV6_FRAGMENT)
		return 1;

	if (hdr[6] != IPPROTO_UDP)
		return -1;

	p->family = FAMILY_IPV6;
	p->tot_len = (((__u16)hdr[4] << 8) | hdr[5]) + 40;
	__builtin_memcpy(p->saddr, &hdr[8], 16);
	__builtin_memcpy(p->daddr, &hdr[24], 16);
	p->l4 = p->l3 + 40;

	return 0;
}

static __always_inline int extension_headers_relayable(struct __sk_buff *skb,
						       __u32 off, __u32 *end)
{
	__u8 next;

	if (bpf_skb_load_bytes(skb, off + 11, &next, 1))
		return 0;

	off += GTP_HLEN + GTP_OPTIONAL_LEN;

	for (int i = 0; i < MAX_EXT_HEADERS; i++) {
		if (next == 0) {
			*end = off;
			return 1;
		}

		if ((next & 0xc0) == 0x40 ||
		    ((next & 0xc0) == 0xc0 && next != 0xc0))
			return 0;

		__u8 len;

		if (bpf_skb_load_bytes(skb, off, &len, 1) || len == 0)
			return 0;

		off += (__u32)len * 4;

		if (bpf_skb_load_bytes(skb, off - 1, &next, 1))
			return 0;
	}

	if (next != 0)
		return 0;

	*end = off;
	return 1;
}

static __always_inline int rewrite_ipv4(struct __sk_buff *skb,
					const struct parsed *p,
					const struct tunnel *t)
{
	__u32 old_src, old_dst, new_src, new_dst;
	__u16 old_ttl_word, new_ttl_word, old_frag, new_frag, old_sport;
	__u8 ttl_word[2];

	if (bpf_skb_load_bytes(skb, p->l3 + 12, &old_src, 4) ||
	    bpf_skb_load_bytes(skb, p->l3 + 16, &old_dst, 4) ||
	    bpf_skb_load_bytes(skb, p->l3 + 8, ttl_word, 2) ||
	    bpf_skb_load_bytes(skb, p->l3 + 6, &old_frag, 2) ||
	    bpf_skb_load_bytes(skb, p->l4, &old_sport, 2))
		return -1;

	__builtin_memcpy(&new_src, t->src, 4);
	__builtin_memcpy(&new_dst, t->dst, 4);
	__builtin_memcpy(&old_ttl_word, ttl_word, 2);
	ttl_word[0] = OUTER_TTL;
	__builtin_memcpy(&new_ttl_word, ttl_word, 2);
	new_frag = old_frag & ~bpf_htons(IPV4_DF);

	__u32 csum_off = p->l4 + 6;
	long err = 0;

	err |= bpf_skb_store_bytes(skb, p->l3 + 12, &new_src, 4, 0);
	err |= bpf_skb_store_bytes(skb, p->l3 + 16, &new_dst, 4, 0);
	err |= bpf_skb_store_bytes(skb, p->l3 + 8, &new_ttl_word, 2, 0);
	err |= bpf_skb_store_bytes(skb, p->l3 + 6, &new_frag, 2, 0);
	err |= bpf_l3_csum_replace(skb, p->l3 + 10, old_src, new_src, 4);
	err |= bpf_l3_csum_replace(skb, p->l3 + 10, old_dst, new_dst, 4);
	err |= bpf_l3_csum_replace(skb, p->l3 + 10, old_ttl_word, new_ttl_word,
				   2);
	err |= bpf_l3_csum_replace(skb, p->l3 + 10, old_frag, new_frag, 2);
	err |= bpf_skb_store_bytes(skb, p->l4, &t->sport, 2, 0);
	err |= bpf_skb_store_bytes(skb, p->gtp + 4, &t->teid, 4, 0);

	if (p->check != 0) {
		err |= bpf_l4_csum_replace(skb, csum_off, old_src, new_src,
					   4 | BPF_F_PSEUDO_HDR |
						   BPF_F_MARK_MANGLED_0);
		err |= bpf_l4_csum_replace(skb, csum_off, old_dst, new_dst,
					   4 | BPF_F_PSEUDO_HDR |
						   BPF_F_MARK_MANGLED_0);
		err |= bpf_l4_csum_replace(skb, csum_off, old_sport, t->sport,
					   2 | BPF_F_MARK_MANGLED_0);
		err |= bpf_l4_csum_replace(skb, csum_off, p->teid, t->teid,
					   4 | BPF_F_MARK_MANGLED_0);
	} else {
		__u32 from[2] = { p->teid, old_sport };
		__u32 to[2] = { t->teid, t->sport };
		__s64 diff =
			bpf_csum_diff(from, sizeof(from), to, sizeof(to), 0);

		if (diff < 0)
			return -1;

		bpf_csum_update(skb, (__wsum)diff);
	}

	return err ? -1 : 0;
}

static __always_inline int rewrite_ipv6(struct __sk_buff *skb,
					const struct parsed *p,
					const struct tunnel *t)
{
	__u8 old_head[40];
	__u8 new_head[40];
	__u16 old_sport;

	if (bpf_skb_load_bytes(skb, p->l3, old_head, sizeof(old_head)) ||
	    bpf_skb_load_bytes(skb, p->l4, &old_sport, 2))
		return -1;

	__builtin_memcpy(new_head, old_head, sizeof(new_head));
	new_head[7] = OUTER_TTL;
	__builtin_memcpy(&new_head[8], t->src, 16);
	__builtin_memcpy(&new_head[24], t->dst, 16);

	__s64 hop = bpf_csum_diff((__be32 *)&old_head[4], 4,
				  (__be32 *)&new_head[4], 4, 0);
	__s64 addrs = bpf_csum_diff((__be32 *)&old_head[8], 32,
				    (__be32 *)&new_head[8], 32, 0);

	if (hop < 0 || addrs < 0)
		return -1;

	__u32 csum_off = p->l4 + 6;
	long err = 0;

	err |= bpf_skb_store_bytes(skb, p->l3, new_head, sizeof(new_head), 0);

	long r = bpf_l4_csum_replace(skb, csum_off, 0, (__u32)addrs,
				     BPF_F_PSEUDO_HDR | BPF_F_IPV6 |
					     BPF_F_MARK_MANGLED_0);

	if (r == -22) {
		r = bpf_l4_csum_replace(skb, csum_off, 0, (__u32)addrs,
					BPF_F_PSEUDO_HDR |
						BPF_F_MARK_MANGLED_0);
		if (!r)
			bpf_csum_update(skb, (__wsum)addrs);
	}

	bpf_csum_update(skb, (__wsum)hop);

	err |= r;
	err |= bpf_skb_store_bytes(skb, p->l4, &t->sport, 2, 0);
	err |= bpf_l4_csum_replace(skb, csum_off, old_sport, t->sport,
				   2 | BPF_F_MARK_MANGLED_0);
	err |= bpf_skb_store_bytes(skb, p->gtp + 4, &t->teid, 4, 0);
	err |= bpf_l4_csum_replace(skb, csum_off, p->teid, t->teid,
				   4 | BPF_F_MARK_MANGLED_0);

	return err ? -1 : 0;
}

static __always_inline int policy_ipsec(void)
{
	__u32 zero = 0;
	__u32 *cfg = bpf_map_lookup_elem(&config, &zero);

	return cfg && (*cfg & CONFIG_POLICY_IPSEC);
}

static __always_inline int set_l2(struct __sk_buff *skb, __u32 l3, __be16 proto,
				  const __u8 *macs)
{
	if (l3 == 0 && bpf_skb_change_head(skb, ETH_HLEN, 0))
		return -1;

	if (bpf_skb_store_bytes(skb, 0, macs, 12, 0) ||
	    bpf_skb_store_bytes(skb, 12, &proto, 2, 0))
		return -1;

	return 0;
}

static __always_inline void account(const struct tunnel *t, __u32 key,
				    const struct parsed *p, int gpdu, __u64 len)
{
	struct tunnel_activity *s = bpf_map_lookup_elem(&tunnel_stats, &key);

	if (gpdu) {
		struct counter *c = bpf_map_lookup_elem(&path_stats, &t->path);

		if (c) {
			c->packets += 1;
			c->bytes += len;
		}

		if (s) {
			__sync_fetch_and_add(&s->packets, 1);
			__sync_fetch_and_add(&s->bytes, len);
		}
	}

	if (s && __builtin_memcmp(s->last, p->saddr, 16))
		__builtin_memcpy(s->last, p->saddr, 16);
}

SEC("tcx/ingress")
int relay(struct __sk_buff *skb)
{
	__u32 ifindex = skb->ingress_ifindex;
	struct iface *in = bpf_map_lookup_elem(&ifaces, &ifindex);

	if (!in || skb->vlan_present)
		return TCX_NEXT;

	struct parsed p = {};
	int rc;

	p.l3 = ETH_HLEN;

	if ((in->flags & IFACE_L3) && network_offset(skb, &p.l3))
		return TCX_NEXT;

	if (skb->protocol == bpf_htons(ETH_P_IP))
		rc = parse_ipv4(skb, &p);
	else if (skb->protocol == bpf_htons(ETH_P_IPV6))
		rc = parse_ipv6(skb, &p);
	else
		return TCX_NEXT;

	if (rc < 0)
		return TCX_NEXT;

	if (rc > 0)
		return pass(PASS_IP_FRAGMENT);

	__u8 udp[8];

	if (bpf_skb_load_bytes(skb, p.l4, udp, sizeof(udp)))
		return TCX_NEXT;

	if (((__u16)udp[2] << 8 | udp[3]) != GTPU_PORT)
		return TCX_NEXT;

	p.check = (__u16)udp[6] << 8 | udp[7];
	p.gtp = p.l4 + 8;

	__u8 gtp[8];

	if (bpf_skb_load_bytes(skb, p.gtp, gtp, sizeof(gtp)))
		return TCX_NEXT;

	if ((gtp[0] >> 5) != 1 || !(gtp[0] & 0x10))
		return TCX_NEXT;

	if (gtp[1] != GTP_GPDU && gtp[1] != GTP_END_MARKER &&
	    gtp[1] != GTP_TUNNEL_STATUS)
		return TCX_NEXT;

	__builtin_memcpy(&p.teid, &gtp[4], 4);

	__u32 key = bpf_ntohl(p.teid);
	struct tunnel *t = bpf_map_lookup_elem(&tunnels, &key);

	if (!t)
		return pass(PASS_NO_TUNNEL);

	if (t->family != p.family)
		return pass(PASS_NOT_LOCAL);

	if (p.family == FAMILY_IPV4) {
		if (__builtin_memcmp(p.daddr, t->local, 4))
			return pass(PASS_NOT_LOCAL);
	} else if (__builtin_memcmp(p.daddr, t->local, 16)) {
		return pass(PASS_NOT_LOCAL);
	}

	if (t->leg >= MAX_LEGS || !(in->legs & (1ULL << t->leg)))
		return drop(t->op, DROP_WRONG_LEG);

	__u32 tpdu = p.gtp + GTP_HLEN;
	__u32 end = tpdu + (((__u32)gtp[2] << 8) | gtp[3]);

	if (gtp[0] & (GTP_FLAG_E | GTP_FLAG_S | GTP_FLAG_PN))
		tpdu += GTP_OPTIONAL_LEN;

	if ((gtp[0] & GTP_FLAG_E) &&
	    !extension_headers_relayable(skb, p.gtp, &tpdu))
		return pass(PASS_EXT_HEADER);

	if (tpdu > end || end > p.l3 + p.tot_len)
		return TCX_NEXT;

	if (p.family == FAMILY_IPV6 && p.check == 0)
		return pass(PASS_ZERO_CHECKSUM);

	if (skb->gso_size)
		return pass(PASS_MERGED);

	int ipsec = policy_ipsec();

	if (ipsec && !(in->flags & IFACE_XFRM))
		return pass(PASS_IPSEC_POLICY);

	struct bpf_fib_lookup fib = {};

	fib.family = p.family;
	fib.ifindex = ifindex;
	fib.l4_protocol = IPPROTO_UDP;
	fib.sport = t->sport;
	fib.dport = bpf_htons(GTPU_PORT);
	fib.tot_len = p.tot_len;

	if (p.family == FAMILY_IPV4) {
		fib.tos = p.tos;
		__builtin_memcpy(&fib.ipv4_src, t->src, 4);
		__builtin_memcpy(&fib.ipv4_dst, t->dst, 4);
	} else {
		__builtin_memcpy(fib.ipv6_src, t->src, 16);
		__builtin_memcpy(fib.ipv6_dst, t->dst, 16);
	}

	long fr = bpf_fib_lookup(skb, &fib, sizeof(fib), 0);

	switch (fr) {
	case BPF_FIB_LKUP_RET_SUCCESS:
	case BPF_FIB_LKUP_RET_NO_NEIGH:
		break;
	case BPF_FIB_LKUP_RET_FRAG_NEEDED:
		return pass(PASS_FRAG_NEEDED);
	case BPF_FIB_LKUP_RET_FWD_DISABLED:
		return pass(PASS_FWD_DISABLED);
	case BPF_FIB_LKUP_RET_BLACKHOLE:
		return drop(t->op, DROP_FIB_BLACKHOLE);
	case BPF_FIB_LKUP_RET_UNREACHABLE:
		return drop(t->op, DROP_FIB_UNREACHABLE);
	case BPF_FIB_LKUP_RET_PROHIBIT:
		return drop(t->op, DROP_FIB_PROHIBIT);
	default:
		return pass(PASS_FIB_OTHER);
	}

	__u32 out_index = fib.ifindex;

	if (out_index != t->out_ifindex)
		return drop(t->op, DROP_WRONG_EGRESS);

	struct iface *out = bpf_map_lookup_elem(&ifaces, &out_index);

	if (!out)
		return drop(t->op, DROP_WRONG_EGRESS);

	if (ipsec && !(out->flags & IFACE_XFRM))
		return pass(PASS_IPSEC_POLICY);

	__u8 op = t->op;
	int gpdu = gtp[1] == GTP_GPDU;
	__u64 len = end - tpdu;
	__be16 proto = skb->protocol;

	if ((p.family == FAMILY_IPV4 ? rewrite_ipv4(skb, &p, t) :
				       rewrite_ipv6(skb, &p, t)) != 0)
		return drop(op, DROP_REWRITE_FAILED);

	long ret;

	if (out->flags & IFACE_L3) {
		ret = bpf_redirect(out_index, 0);
	} else if (fr == BPF_FIB_LKUP_RET_NO_NEIGH) {
		__u8 zero[12] = {};
		struct bpf_redir_neigh nh = {};

		if (set_l2(skb, p.l3, proto, zero))
			return drop(op, DROP_REWRITE_FAILED);

		nh.nh_family = fib.family;

		if (fib.family == FAMILY_IPV4)
			nh.ipv4_nh = fib.ipv4_dst;
		else
			__builtin_memcpy(nh.ipv6_nh, fib.ipv6_dst, 16);

		ret = bpf_redirect_neigh(out_index, &nh, sizeof(nh), 0);
	} else {
		__u8 macs[12];

		__builtin_memcpy(macs, fib.dmac, 6);
		__builtin_memcpy(&macs[6], fib.smac, 6);

		if (set_l2(skb, p.l3, proto, macs))
			return drop(op, DROP_REWRITE_FAILED);

		ret = bpf_redirect(out_index, 0);
	}

	if (ret == TCX_REDIRECT)
		account(t, key, &p, gpdu, len);

	return ret;
}

char _license[] SEC("license") = "GPL";
