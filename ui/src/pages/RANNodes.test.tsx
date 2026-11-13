import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import RANNodes from "@/pages/RANNodes";
import type { RANNode } from "@/queries/ran_nodes";
import { json, readOnlyAuth, renderWithClient, stubApi } from "@/test/render";

const NODES: RANNode[] = [
  {
    global_enb_id: {
      plmn: { mcc: "001", mnc: "01" },
      enb_id_type: "macro",
      enb_id: 1,
    },
    enb_name: "srsenb01",
    supported_tas: [
      {
        tac: 1,
        broadcast_plmns: [
          { mcc: "001", mnc: "01" },
          { mcc: "315", mnc: "010" },
        ],
      },
      { tac: 7, broadcast_plmns: [{ mcc: "001", mnc: "01" }] },
    ],
    default_paging_drx: 128,
    state: "up",
    connected_since: "2026-11-12T10:00:00Z",
    tnl_addresses: ["10.0.0.5", "10.0.0.6"],
  },
  {
    global_enb_id: {
      plmn: { mcc: "315", mnc: "010" },
      enb_id_type: "home",
      enb_id: 4660,
    },
    supported_tas: [{ tac: 2, broadcast_plmns: [{ mcc: "999", mnc: "99" }] }],
    default_paging_drx: 64,
    state: "connected",
    connected_since: "2026-11-12T10:00:05Z",
    tnl_addresses: ["10.0.0.7"],
    last_failure_cause: "misc: unknown-PLMN (5)",
  },
  {
    global_enb_id: null,
    supported_tas: [],
    state: "connected",
    connected_since: "2026-11-12T10:00:09Z",
    tnl_addresses: ["10.0.0.8"],
  },
];

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const serve = (nodes: () => RANNode[]) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(
      stubApi({
        "/api/v1/ran-nodes": () => json(200, { result: { items: nodes() } }),
      }),
    ),
  );

const renderPage = () =>
  renderWithClient(
    <MemoryRouter>
      <RANNodes />
    </MemoryRouter>,
    readOnlyAuth,
  );

const rows = () =>
  within(screen.getByRole("grid", { name: "RAN Nodes" }))
    .getAllByRole("row")
    .slice(1)
    .map((row) =>
      within(row)
        .getAllByRole("gridcell")
        .map((cell) => cell.textContent),
    );

describe("RANNodes", () => {
  it("lists the RAN nodes with what they sent in S1 Setup", async () => {
    serve(() => NODES);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "RAN Nodes (3)" }),
    ).toBeInTheDocument();
    expect(document.title).toBe("RAN Nodes · Ella MOCN");
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual([
      "Global eNB ID",
      "eNB name",
      "Supported TAs",
      "Paging DRX",
      "State",
      "Connected since",
      "TNL addresses",
      "Last failure",
    ]);
    const [up, rejected, pending] = rows();
    expect(up).toEqual([
      "001/01 · Macro (20 bits) · 1",
      "srsenb01",
      "TAC 1: 001/01, 315/010TAC 7: 001/01",
      "128",
      "Up",
      expect.stringMatching(/^Nov 12, \d\d:\d\d:00$/),
      "10.0.0.5, 10.0.0.6",
      "—",
    ]);
    expect(rejected).toEqual([
      "315/010 · Home (28 bits) · 4660",
      "—",
      "TAC 2: 999/99",
      "64",
      "Connected",
      expect.stringMatching(/^Nov 12, \d\d:\d\d:05$/),
      "10.0.0.7",
      "misc: unknown-PLMN (5)",
    ]);
    expect(pending).toEqual([
      "—",
      "—",
      "—",
      "—",
      "Connected",
      expect.stringMatching(/^Nov 12, \d\d:\d\d:09$/),
      "10.0.0.8",
      "—",
    ]);
    expect(
      within(screen.getAllByRole("list", { name: "Supported TAs" })[0])
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual(["TAC 1: 001/01, 315/010", "TAC 7: 001/01"]);
  });

  it("explains each column on hover", async () => {
    serve(() => NODES);

    renderPage();
    await screen.findByRole("heading", { level: 1, name: "RAN Nodes (3)" });

    fireEvent.mouseOver(screen.getByText("Supported TAs"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The tracking areas the RAN node serves, each with the PLMNs it broadcasts.",
      }),
    ).toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    serve(() => []);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "RAN Nodes (0)" }),
    ).toBeInTheDocument();
    expect(screen.getByText("No RAN nodes.")).toBeInTheDocument();
  });

  it("refreshes the list", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let nodes: RANNode[] = [];
    serve(() => nodes);

    renderPage();
    expect(await screen.findByText("No RAN nodes.")).toBeInTheDocument();

    nodes = [NODES[0]];
    await vi.advanceTimersByTimeAsync(5000);

    await waitFor(() => expect(screen.getByText("srsenb01")).toBeVisible());
    expect(screen.queryByText("No RAN nodes.")).not.toBeInTheDocument();
  });

  it("says when the RAN nodes fail to load", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        stubApi({
          "/api/v1/ran-nodes": () => json(500, { error: "boom" }),
        }),
      ),
    );

    renderPage();

    expect(
      await screen.findByText("Failed to load RAN nodes"),
    ).toBeInTheDocument();
  });
});
