import { describe, expect, it } from "vitest";
import { parseMetrics, sumOf } from "@/queries/metrics";

const TEXT = `# HELP ellamocn_peer_requests_total Requests to peers.
# TYPE ellamocn_peer_requests_total counter
ellamocn_peer_requests_total{interface="cx",result="success"} 10
ellamocn_peer_requests_total{interface="cx",result="timeout"} 2
ellamocn_peer_requests_total{interface="rx",result="success"} 5
ellamocn_build_info{version="v1.2.3",note="a \\"quoted\\" value"} 1
process_resident_memory_bytes 6.7108864e+07
go_gc_duration_seconds{quantile="0.5"} NaN
malformed line
`;

describe("parseMetrics", () => {
  it("parses the samples and skips comments, NaN and malformed lines", () => {
    expect(parseMetrics(TEXT)).toEqual([
      {
        name: "ellamocn_peer_requests_total",
        labels: { interface: "cx", result: "success" },
        value: 10,
      },
      {
        name: "ellamocn_peer_requests_total",
        labels: { interface: "cx", result: "timeout" },
        value: 2,
      },
      {
        name: "ellamocn_peer_requests_total",
        labels: { interface: "rx", result: "success" },
        value: 5,
      },
      {
        name: "ellamocn_build_info",
        labels: { version: "v1.2.3", note: 'a \\"quoted\\" value' },
        value: 1,
      },
      {
        name: "process_resident_memory_bytes",
        labels: {},
        value: 67108864,
      },
    ]);
  });
});

describe("sumOf", () => {
  const samples = parseMetrics(TEXT);

  it("sums the samples of a metric", () => {
    expect(sumOf(samples, "ellamocn_peer_requests_total")).toBe(17);
  });

  it("sums the samples that have the labels", () => {
    expect(
      sumOf(samples, "ellamocn_peer_requests_total", { interface: "cx" }),
    ).toBe(12);
  });

  it("returns undefined for a metric absent", () => {
    expect(sumOf(samples, "ellamocn_active_calls")).toBeUndefined();
  });
});
