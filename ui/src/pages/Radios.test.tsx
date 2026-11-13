import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import RadioDetail from "@/pages/RadioDetail";
import Radios from "@/pages/Radios";
import type { Radio } from "@/queries/radios";
import { json, readOnlyAuth, renderWithClient, stubApi } from "@/test/render";

const NODES: Radio[] = [
  {
    ref: "001-01:macro:1",
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
    ue_connections: 2,
    ue_connections_by_operator: [
      { operator: "Operator A", ue_connections: 2 },
      { operator: "Operator B", ue_connections: 0 },
    ],
  },
  {
    ref: "315-010:home:4660",
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
    ue_connections: 0,
  },
  {
    global_enb_id: null,
    supported_tas: [],
    state: "connected",
    connected_since: "2026-11-12T10:00:09Z",
    tnl_addresses: ["10.0.0.8"],
    ue_connections: 0,
  },
];

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const serve = (nodes: () => Radio[]) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(
      stubApi({
        "/api/v1/radios": () => json(200, { result: { items: nodes() } }),
        ...Object.fromEntries(
          nodes()
            .filter((n) => n.ref)
            .map((n) => [
              `/api/v1/radios/${encodeURIComponent(n.ref!)}`,
              () => json(200, { result: n }),
            ]),
        ),
      }),
    ),
  );

function Location() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

const renderAt = (path: string) =>
  renderWithClient(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/radios" element={<Radios />} />
        <Route path="/radios/:ref" element={<RadioDetail />} />
      </Routes>
      <Location />
    </MemoryRouter>,
    readOnlyAuth,
  );

const renderPage = () => renderAt("/radios");

const rows = () =>
  within(screen.getByRole("grid", { name: "Radios" }))
    .getAllByRole("row")
    .slice(1)
    .map((row) =>
      within(row)
        .getAllByRole("gridcell")
        .map((cell) => cell.textContent),
    );

describe("Radios", () => {
  it("lists the radios with what they sent in S1 Setup", async () => {
    serve(() => NODES);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Radios (3)" }),
    ).toBeInTheDocument();
    expect(document.title).toBe("Radios · Ella MOCN");
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual([
      "Name",
      "Global eNB ID",
      "Addresses",
      "UE connections",
      "Status",
    ]);
    expect(rows()).toEqual([
      [
        "srsenb01",
        "001/01 · Macro (20 bits) · 1",
        "10.0.0.5, 10.0.0.6",
        "2",
        "Up",
      ],
      [
        "315-010:home:4660",
        "315/010 · Home (28 bits) · 4660",
        "10.0.0.7",
        "0",
        "Connected",
      ],
      ["10.0.0.8", "—", "10.0.0.8", "0", "Connected"],
    ]);
    expect(screen.getByRole("link", { name: "srsenb01" })).toHaveAttribute(
      "href",
      "/radios/001-01%3Amacro%3A1",
    );
    expect(
      screen.queryByRole("link", { name: "10.0.0.8" }),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("grid", { name: "Radios" }))
        .getByText("Up")
        .closest(".MuiChip-root"),
    ).toHaveAttribute(
      "title",
      expect.stringMatching(/^Since Nov 12, \d\d:\d\d:00$/),
    );
  });

  it("opens a radio's page from its row", async () => {
    serve(() => NODES);

    renderPage();
    fireEvent.click(await screen.findByText("001/01 · Macro (20 bits) · 1"));

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Radios\s*\/\s*srsenb01$/,
      }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("location").textContent).toBe(
      "/radios/001-01%3Amacro%3A1",
    );
  });

  it("shows a radio's details", async () => {
    serve(() => NODES);

    renderAt("/radios/001-01%3Amacro%3A1");

    expect(
      await screen.findByRole("heading", { level: 2, name: "Overview" }),
    ).toBeInTheDocument();
    const overview = within(screen.getByRole("table", { name: "Overview" }));
    const valueOf = (label: string) =>
      overview.getByText(label).closest("tr")!.querySelectorAll("td")[1]
        .textContent;
    expect(valueOf("Name")).toBe("srsenb01");
    expect(valueOf("Global eNB ID")).toBe("001/01 · Macro (20 bits) · 1");
    expect(valueOf("Status")).toBe("Up");
    expect(valueOf("Connected since")).toMatch(/^Nov 12, \d\d:\d\d:00$/);
    expect(valueOf("Addresses")).toBe("10.0.0.5, 10.0.0.6");
    expect(valueOf("Paging DRX")).toBe("128");

    expect(valueOf("Supported TAs")).toBe(
      "TAC 1: 001/01, 315/010; TAC 7: 001/01",
    );
    expect(
      overview.getByTitle("TAC 1: 001/01, 315/010; TAC 7: 001/01"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("grid", { name: /^Supported TAs/ }),
    ).not.toBeInTheDocument();

    fireEvent.mouseOver(overview.getByText("Supported TAs"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The tracking areas the radio serves, each with the PLMNs it broadcasts.",
      }),
    ).toBeInTheDocument();
  });

  it("shows a dash for a radio without tracking areas", async () => {
    const node: Radio = { ...NODES[0], supported_tas: [] };
    serve(() => [node]);

    renderAt("/radios/001-01%3Amacro%3A1");

    const overview = within(
      await screen.findByRole("table", { name: "Overview" }),
    );
    expect(
      overview
        .getByText("Supported TAs")
        .closest("tr")!
        .querySelectorAll("td")[1].textContent,
    ).toBe("—");
  });

  it("shows a radio's UE connections by operator", async () => {
    serve(() => NODES);

    renderAt("/radios/001-01%3Amacro%3A1");

    const grid = within(
      await screen.findByRole("grid", { name: "UE connections by operator" }),
    );
    expect(grid.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(
      ["Operator", "UE connections"],
    );
    expect(
      grid
        .getAllByRole("row")
        .slice(1)
        .map((row) =>
          within(row)
            .getAllByRole("gridcell")
            .map((cell) => cell.textContent),
        ),
    ).toEqual([
      ["Operator A", "2"],
      ["Operator B", "0"],
    ]);
    expect(grid.getByRole("link", { name: "Operator B" })).toHaveAttribute(
      "href",
      "/operators/Operator%20B",
    );

    fireEvent.mouseOver(grid.getByText("Operator"));
    expect(
      await screen.findByRole("tooltip", {
        name: "An operator that owns a PLMN the radio broadcasts, or that has UE connections from it.",
      }),
    ).toBeInTheDocument();
  });

  it("keeps a radio's UE connections by operator up to date", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const node: Radio = { ...NODES[0], ue_connections_by_operator: [] };
    serve(() => [node]);

    renderAt("/radios/001-01%3Amacro%3A1");
    const grid = await screen.findByRole("grid", {
      name: "UE connections by operator",
    });
    expect(within(grid).getByText("No operators.")).toBeInTheDocument();

    node.ue_connections_by_operator = [
      { operator: "Operator A", ue_connections: 1 },
    ];
    await vi.advanceTimersByTimeAsync(5000);

    await waitFor(() =>
      expect(within(grid).getByText("Operator A")).toBeVisible(),
    );
  });

  it("says when a radio is gone", async () => {
    serve(() => NODES);

    renderAt("/radios/001-01%3Amacro%3A99");

    expect(
      await screen.findByText("Failed to load this radio"),
    ).toBeInTheDocument();
  });

  it("explains each column on hover", async () => {
    serve(() => NODES);

    renderPage();
    await screen.findByRole("heading", { level: 1, name: "Radios (3)" });

    fireEvent.mouseOver(screen.getByText("Global eNB ID"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The identity the radio sent in its S1 Setup Request: PLMN, eNB ID type and eNB ID.",
      }),
    ).toBeInTheDocument();
  });

  it("shows an empty state", async () => {
    serve(() => []);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Radios (0)" }),
    ).toBeInTheDocument();
    expect(screen.getByText("No radios.")).toBeInTheDocument();
  });

  it("refreshes the list", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let nodes: Radio[] = [];
    serve(() => nodes);

    renderPage();
    expect(await screen.findByText("No radios.")).toBeInTheDocument();

    nodes = [NODES[0]];
    await vi.advanceTimersByTimeAsync(5000);

    await waitFor(() => expect(screen.getByText("srsenb01")).toBeVisible());
    expect(screen.queryByText("No radios.")).not.toBeInTheDocument();
  });

  it("says when the radios fail to load", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        stubApi({
          "/api/v1/radios": () => json(500, { error: "boom" }),
        }),
      ),
    );

    renderPage();

    expect(
      await screen.findByText("Failed to load radios"),
    ).toBeInTheDocument();
  });
});
