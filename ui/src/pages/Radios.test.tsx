import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import RadioDetail from "@/pages/RadioDetail";
import Radios from "@/pages/Radios";
import type { Radio } from "@/queries/radios";
import { json, readOnlyAuth, renderWithClient, stubApi } from "@/test/render";

const NODES: Radio[] = [
  {
    ref: "eNB:001-01:MacroeNB-00001",
    type: "eNB",
    global_enb_id: {
      plmn: { mcc: "001", mnc: "01" },
      enb_id_type: "macro",
      enb_id: 1,
    },
    name: "srsenb01",
    supported_tas: [
      {
        tac: "000001",
        broadcast_plmns: [
          { plmn: { mcc: "001", mnc: "01" } },
          { plmn: { mcc: "315", mnc: "010" } },
        ],
      },
      { tac: "000007", broadcast_plmns: [{ plmn: { mcc: "001", mnc: "01" } }] },
    ],
    default_paging_drx: 128,
    state: "up",
    connected_at: "2026-11-12T10:00:00Z",
    tnl_addresses: ["10.0.0.5", "10.0.0.6"],
    ue_connections: 2,
    ue_connections_by_operator: [
      { operator: "Operator A", ue_connections: 2 },
      { operator: "Operator B", ue_connections: 0 },
    ],
  },
  {
    ref: "eNB:315-010:HomeeNB-0001234",
    type: "eNB",
    global_enb_id: {
      plmn: { mcc: "315", mnc: "010" },
      enb_id_type: "home",
      enb_id: 4660,
    },
    supported_tas: [
      { tac: "000002", broadcast_plmns: [{ plmn: { mcc: "999", mnc: "99" } }] },
    ],
    default_paging_drx: 64,
    state: "connected",
    connected_at: "2026-11-12T10:00:05Z",
    tnl_addresses: ["10.0.0.7"],
    ue_connections: 0,
  },
  {
    type: "eNB",
    supported_tas: [],
    state: "connected",
    connected_at: "2026-11-12T10:00:09Z",
    tnl_addresses: ["10.0.0.8"],
    ue_connections: 0,
  },
  {
    ref: "gNB:001-01:00019c@24",
    type: "gNB",
    global_gnb_id: {
      plmn: { mcc: "001", mnc: "01" },
      gnb_id: 412,
      bit_length: 24,
    },
    name: "srsgnb01",
    supported_tas: [
      {
        tac: "010203",
        broadcast_plmns: [
          {
            plmn: { mcc: "001", mnc: "01" },
            snssais: [{ sst: 1 }, { sst: 2, sd: "000001" }],
          },
        ],
      },
    ],
    default_paging_drx: 128,
    state: "up",
    connected_at: "2026-11-12T10:00:10Z",
    tnl_addresses: ["10.0.0.9"],
    ue_connections: 1,
    ue_connections_by_operator: [{ operator: "Operator A", ue_connections: 1 }],
  },
];

const REFUSED: Radio = {
  type: "gNB",
  global_gnb_id: {
    plmn: { mcc: "001", mnc: "03" },
    gnb_id: 2,
    bit_length: 22,
  },
  name: "gnb-west",
  supported_tas: [
    {
      tac: "000001",
      broadcast_plmns: [
        { plmn: { mcc: "001", mnc: "03" }, snssais: [{ sst: 1 }] },
      ],
    },
  ],
  default_paging_drx: 128,
  state: "setup_failed",
  setup_failure: {
    cause: "Unknown PLMN or SNPN",
    reason: "no operator serves a broadcast PLMN",
    at: "2026-11-12T10:00:21Z",
  },
  connected_at: "2026-11-12T10:00:20Z",
  tnl_addresses: ["10.0.0.10"],
  ue_connections: 0,
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

const serve = (nodes: () => Radio[]) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(
      stubApi({
        "/api/v1/radios": (url) => {
          const page = Number(url.searchParams.get("page"));
          const perPage = Number(url.searchParams.get("per_page"));
          const all = nodes();
          return json(200, {
            result: {
              items: all.slice((page - 1) * perPage, page * perPage),
              page,
              per_page: perPage,
              total_count: all.length,
            },
          });
        },
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
      await screen.findByRole("heading", { level: 1, name: "Radios (4)" }),
    ).toBeInTheDocument();
    expect(document.title).toBe("Radios · Ella MOCN");
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual([
      "Name",
      "Type",
      "Global RAN Node ID",
      "Addresses",
      "UE Connections",
      "Status",
    ]);
    expect(rows()).toEqual([
      [
        "srsenb01",
        "eNB (4G)",
        "001-01 · Macro (20 bits) · 1",
        "10.0.0.5, 10.0.0.6",
        "2",
        "Up",
      ],
      [
        "eNB 4660",
        "eNB (4G)",
        "315-010 · Home (28 bits) · 4660",
        "10.0.0.7",
        "0",
        "Connected",
      ],
      ["10.0.0.8", "eNB (4G)", "—", "10.0.0.8", "0", "Connected"],
      ["srsgnb01", "gNB (5G)", "001-01 · 412 (24 bits)", "10.0.0.9", "1", "Up"],
    ]);
    expect(screen.getByRole("link", { name: "srsgnb01" })).toHaveAttribute(
      "href",
      "/radios/gNB%3A001-01%3A00019c%4024",
    );
    expect(screen.getByRole("link", { name: "srsenb01" })).toHaveAttribute(
      "href",
      "/radios/eNB%3A001-01%3AMacroeNB-00001",
    );
    expect(screen.getByRole("link", { name: "eNB 4660" })).toHaveAttribute(
      "href",
      "/radios/eNB%3A315-010%3AHomeeNB-0001234",
    );
    expect(
      screen.queryByRole("link", { name: "10.0.0.8" }),
    ).not.toBeInTheDocument();
    expect(
      within(screen.getByRole("grid", { name: "Radios" }))
        .getAllByText("Up")[0]
        .closest(".MuiChip-root"),
    ).toHaveAttribute(
      "title",
      expect.stringMatching(/^Connected at Nov 12, \d\d:\d\d:00$/),
    );
  });

  it("shows a radio whose setup failed, with the reason", async () => {
    serve(() => [NODES[0], REFUSED]);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Radios (2)" }),
    ).toBeInTheDocument();
    expect(rows()[1]).toEqual([
      "gnb-west",
      "gNB (5G)",
      "001-03 · 2 (22 bits)",
      "10.0.0.10",
      "0",
      "Setup failed",
    ]);
    expect(
      within(screen.getByRole("grid", { name: "Radios" }))
        .getByText("Setup failed")
        .closest(".MuiChip-root"),
    ).toHaveAttribute(
      "title",
      expect.stringMatching(
        /^Unknown PLMN or SNPN: no operator serves a broadcast PLMN\. Refused at Nov 12, \d\d:\d\d:21$/,
      ),
    );
    expect(
      screen.queryByRole("link", { name: "gnb-west" }),
    ).not.toBeInTheDocument();
  });

  it("opens the details of a radio without a ref in a dialog", async () => {
    serve(() => [REFUSED]);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "gnb-west" }));

    const dialog = within(
      await screen.findByRole("dialog", { name: "gnb-west" }),
    );
    const overview = within(dialog.getByRole("table", { name: "Overview" }));
    const valueOf = (label: string) =>
      overview.getByText(label).closest("tr")!.querySelectorAll("td")[1]
        .textContent;
    expect(valueOf("Global gNB ID")).toBe("001-03 · 2 (22 bits)");
    expect(valueOf("Status")).toBe(
      "Setup failedUnknown PLMN or SNPN: no operator serves a broadcast PLMN",
    );
    expect(valueOf("Supported TAs")).toBe("TAC 000001: 001-03 (SST 1)");
    expect(screen.getByTestId("location").textContent).toBe("/radios");

    fireEvent.click(dialog.getByRole("button", { name: "Close" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("keeps a radio's dialog open while the radio redials", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let nodes: Radio[] = [REFUSED];
    serve(() => nodes);

    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "gnb-west" }));
    await screen.findByRole("dialog", { name: "gnb-west" });

    nodes = [];
    await vi.advanceTimersByTimeAsync(5000);
    await waitFor(() => expect(screen.getByText("No radios.")).toBeVisible());
    expect(screen.getByRole("dialog", { name: "gnb-west" })).toBeVisible();

    nodes = [{ ...REFUSED, connected_at: "2026-11-12T10:00:30Z" }];
    await vi.advanceTimersByTimeAsync(5000);
    const dialog = within(screen.getByRole("dialog", { name: "gnb-west" }));
    await waitFor(() =>
      expect(
        dialog
          .getByText("Connected At")
          .closest("tr")!
          .querySelectorAll("td")[1].textContent,
      ).toMatch(/^Nov 12, \d\d:\d\d:30$/),
    );
  });

  it("opens a radio's page from its name", async () => {
    serve(() => NODES);

    renderPage();
    fireEvent.click(await screen.findByRole("link", { name: "srsenb01" }));

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Radios\s*\/\s*srsenb01$/,
      }),
    ).toBeInTheDocument();
    expect(screen.getByTestId("location").textContent).toBe(
      "/radios/eNB%3A001-01%3AMacroeNB-00001",
    );
  });

  it("names an unnamed radio by its type and ID", async () => {
    serve(() => NODES);

    renderAt("/radios/eNB%3A315-010%3AHomeeNB-0001234");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Radios\s*\/\s*eNB 4660$/,
      }),
    ).toBeInTheDocument();
  });

  it("shows a radio's details", async () => {
    serve(() => NODES);

    renderAt("/radios/eNB%3A001-01%3AMacroeNB-00001");

    expect(
      await screen.findByRole("heading", { level: 2, name: "Overview" }),
    ).toBeInTheDocument();
    const overview = within(screen.getByRole("table", { name: "Overview" }));
    const valueOf = (label: string) =>
      overview.getByText(label).closest("tr")!.querySelectorAll("td")[1]
        .textContent;
    expect(valueOf("Name")).toBe("srsenb01");
    expect(valueOf("Type")).toBe("eNB (4G)");
    expect(valueOf("Global eNB ID")).toBe("001-01 · Macro (20 bits) · 1");
    expect(valueOf("Status")).toBe("Up");
    expect(valueOf("Connected At")).toMatch(/^Nov 12, \d\d:\d\d:00$/);
    expect(valueOf("Addresses")).toBe("10.0.0.5, 10.0.0.6");
    expect(valueOf("Default Paging DRX")).toBe("128");

    expect(valueOf("Supported TAs")).toBe(
      "TAC 000001: 001-01, 315-010; TAC 000007: 001-01",
    );
    expect(
      overview.getByTitle("TAC 000001: 001-01, 315-010; TAC 000007: 001-01"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("grid", { name: /^Supported TAs/ }),
    ).not.toBeInTheDocument();

    fireEvent.mouseOver(overview.getByText("Supported TAs"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The tracking areas the radio serves (TAC), each with the networks (PLMNs) it broadcasts.",
      }),
    ).toBeInTheDocument();
  });

  it("shows a dash for a radio without tracking areas", async () => {
    const node: Radio = { ...NODES[0], supported_tas: [] };
    serve(() => [node]);

    renderAt("/radios/eNB%3A001-01%3AMacroeNB-00001");

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

  it("shows a radio's UE Connections by Operator", async () => {
    serve(() => NODES);

    renderAt("/radios/eNB%3A001-01%3AMacroeNB-00001");

    const grid = within(
      await screen.findByRole("grid", { name: "UE Connections by Operator" }),
    );
    expect(grid.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(
      ["Operator", "UE Connections"],
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
        name: "An operator whose network the radio broadcasts, or that has devices connected through it.",
      }),
    ).toBeInTheDocument();
  });

  it("keeps a radio's UE Connections by Operator up to date", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const node: Radio = { ...NODES[0], ue_connections_by_operator: [] };
    serve(() => [node]);

    renderAt("/radios/eNB%3A001-01%3AMacroeNB-00001");
    const grid = await screen.findByRole("grid", {
      name: "UE Connections by Operator",
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

  it("shows a gNB's details", async () => {
    serve(() => NODES);

    renderAt("/radios/gNB%3A001-01%3A00019c%4024");

    expect(
      await screen.findByRole("heading", { level: 2, name: "Overview" }),
    ).toBeInTheDocument();
    const overview = within(screen.getByRole("table", { name: "Overview" }));
    const valueOf = (label: string) =>
      overview.getByText(label).closest("tr")!.querySelectorAll("td")[1]
        .textContent;
    expect(valueOf("Name")).toBe("srsgnb01");
    expect(valueOf("Type")).toBe("gNB (5G)");
    expect(valueOf("Global gNB ID")).toBe("001-01 · 412 (24 bits)");
    expect(valueOf("Supported TAs")).toBe(
      "TAC 010203: 001-01 (SST 1, SST 2 SD 000001)",
    );
    expect(
      screen.getByText("What this radio is and whether it is up."),
    ).toBeInTheDocument();
    fireEvent.mouseOver(overview.getByText("Status"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Up: connected and ready. Connected: link up, setup not done yet (S1 Setup or NG Setup). Setup failed: link up, Ella MOCN refused the radio's last setup.",
      }),
    ).toBeInTheDocument();
    expect(
      within(
        screen.getByRole("grid", { name: "UE Connections by Operator" }),
      ).getByRole("link", { name: "Operator A" }),
    ).toBeInTheDocument();
  });

  it("says when a radio is gone", async () => {
    serve(() => NODES);

    renderAt("/radios/eNB%3A001-01%3AMacroeNB-00063");

    expect(
      await screen.findByText("Failed to load this radio"),
    ).toBeInTheDocument();
  });

  it("explains each column on hover", async () => {
    serve(() => NODES);

    renderPage();
    await screen.findByRole("heading", { level: 1, name: "Radios (4)" });

    fireEvent.mouseOver(screen.getByText("Global RAN Node ID"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The identity the radio sent when it connected: its PLMN, the type of its ID and its ID (Global eNB ID or Global gNB ID).",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(screen.getByText("UE Connections"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Devices connected through this radio: one per active phone, over 4G (S1) or 5G (NG).",
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
    expect(
      screen.queryByRole("grid", { name: "Radios" }),
    ).not.toBeInTheDocument();
  });

  it("pages through the radios", async () => {
    const many: Radio[] = Array.from({ length: 30 }, (_, i) => ({
      ...NODES[2],
      tnl_addresses: [`10.0.1.${i}`],
    }));
    serve(() => many);

    renderPage();

    expect(
      await screen.findByRole("heading", { level: 1, name: "Radios (30)" }),
    ).toBeInTheDocument();
    expect(rows()).toHaveLength(25);
    fireEvent.click(screen.getByRole("button", { name: "Go to next page" }));
    await waitFor(() => expect(rows()).toHaveLength(5));
    expect(rows()[0][0]).toBe("10.0.1.25");
  });
});
