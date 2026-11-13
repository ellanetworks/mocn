import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import Dashboard, { formatBytes } from "@/pages/Dashboard";
import { renderWithClient, stubApi } from "@/test/render";

const METRICS = `# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 6.7108864e+07
process_start_time_seconds 1.7e+09
ellamocn_database_storage_bytes{file="main"} 1048576
ellamocn_database_storage_bytes{file="wal"} 524288
# TYPE ellamocn_radio_associations gauge
ellamocn_radio_associations{interface="s1",state="connected"} 4
ellamocn_radio_associations{interface="s1",state="up"} 2
# TYPE ellamocn_core_associations gauge
ellamocn_core_associations{interface="s1",operator="B",state="connected"} 0
ellamocn_core_associations{interface="s1",operator="B",state="disconnected"} 1
ellamocn_core_associations{interface="s1",operator="B",state="setup_failed"} 0
ellamocn_core_associations{interface="s1",operator="B",state="up"} 1
ellamocn_core_associations{interface="s1",operator="A",state="connected"} 1
ellamocn_core_associations{interface="s1",operator="A",state="disconnected"} 0
ellamocn_core_associations{interface="s1",operator="A",state="setup_failed"} 0
ellamocn_core_associations{interface="s1",operator="A",state="up"} 2
# TYPE ellamocn_ue_connections gauge
ellamocn_ue_connections{interface="s1",operator="B"} 3
ellamocn_ue_connections{interface="s1",operator="A"} 4
ellamocn_ue_connections{interface="s1",operator="C"} 0
`;

afterEach(() => {
  vi.unstubAllGlobals();
});

const OPERATORS = ["A", "B", "C"].map((name) => ({
  name,
  pws_operator: false,
  plmns: [],
  global_enb_id: null,
  global_gnb_id: null,
  mmes: [],
  amfs: [],
}));

const serve = (
  metrics: () => Response,
  operators: () => Response = () =>
    new Response(JSON.stringify({ result: { items: OPERATORS } }), {
      status: 200,
    }),
) =>
  vi.stubGlobal(
    "fetch",
    vi.fn(
      stubApi({ "/api/v1/metrics": metrics, "/api/v1/operators": operators }),
    ),
  );

const renderDashboard = () =>
  renderWithClient(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );

const findPanel = async (title: string) =>
  (await screen.findByRole("heading", { level: 3, name: title })).closest(
    ".MuiCard-root",
  ) as HTMLElement;

describe("Dashboard", () => {
  it("shows the system figures from the metrics", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    const memory = await findPanel("Process Memory");
    expect(await within(memory).findByText("64 MiB")).toBeVisible();
    expect(
      within(await findPanel("Database Size")).getByText("1.5 MiB"),
    ).toBeVisible();
    expect(within(await findPanel("Up Since")).getByText(/2023/)).toBeVisible();
  });

  it("shows the radios with S1 Setup done and links to the radios", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    const radios = await findPanel("Radios");
    expect(await within(radios).findByText("2")).toBeVisible();
    expect(radios.closest("a")).toHaveAttribute("href", "/radios");
  });

  it("shows the UE connections across every operator", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    expect(
      await within(await findPanel("UE connections")).findByText("7"),
    ).toBeVisible();
  });

  it("lists the operators, sorted by name, with a total", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    const table = await screen.findByRole("table", { name: "Operators" });
    const rows = within(table)
      .getAllByRole("row")
      .map((row) =>
        Array.from(row.querySelectorAll("th, td"), (c) => c.textContent),
      );
    expect(rows).toEqual([
      ["Operator", "Core nodes up", "UE connections"],
      ["A", "2 / 3", "4"],
      ["B", "1 / 2", "3"],
      ["C", "0 / 0", "0"],
      ["Total", "3 / 5", "7"],
    ]);
    expect(within(table).getByRole("link", { name: "A" })).toHaveAttribute(
      "href",
      "/operators/A",
    );
    expect(within(table).getByRole("link", { name: "C" })).toHaveAttribute(
      "href",
      "/operators/C",
    );
    expect(
      within(await findPanel("Core nodes and UE connections")).getByRole(
        "link",
        { name: "Core nodes and UE connections" },
      ),
    ).toHaveAttribute("href", "/operators");
  });

  it("counts the operators and links to them", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    const card = await findPanel("Operators");
    expect(await within(card).findByText("3")).toBeVisible();
    expect(card.closest("a") ?? within(card).getByRole("link")).toHaveAttribute(
      "href",
      "/operators",
    );
  });

  it("says when there are no operators", async () => {
    serve(
      () =>
        new Response(
          `ellamocn_radio_associations{interface="s1",state="connected"} 0
ellamocn_radio_associations{interface="s1",state="up"} 0
`,
          { status: 200 },
        ),
    );
    renderDashboard();

    const operators = await findPanel("Core nodes and UE connections");
    expect(await within(operators).findByText("No operators.")).toBeVisible();
    expect(within(operators).queryByRole("table")).not.toBeInTheDocument();
    expect(
      within(await findPanel("UE connections")).getByText("0"),
    ).toBeVisible();
  });

  it("puts the operators table in the overview, before the system", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    await screen.findByRole("region", { name: "Overview" });
    expect(
      screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent),
    ).toEqual(["Overview", "System"]);
    expect(
      within(screen.getByRole("region", { name: "Overview" }))
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent),
    ).toEqual([
      "Operators",
      "Radios",
      "UE connections",
      "Core nodes and UE connections",
    ]);
  });

  it("explains the radios figure on hover", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    fireEvent.mouseOver(await findPanel("Radios"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Radios that completed S1 Setup or NG Setup with Ella MOCN.",
      }),
    ).toBeInTheDocument();
  });

  it("says what the UE connections figure counts on hover", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    fireEvent.mouseOver(await findPanel("UE connections"));
    expect(
      await screen.findByRole("tooltip", {
        name: "UE-associated logical S1-connections and NG-connections that Ella MOCN relays, across every operator: one per connected UE.",
      }),
    ).toBeInTheDocument();
  });

  it("shows N/A for a metric the MOCN does not expose", async () => {
    serve(() => new Response("", { status: 200 }));
    renderDashboard();

    const database = await findPanel("Database Size");
    expect(await within(database).findByText("N/A")).toBeInTheDocument();
    expect(
      within(await findPanel("Radios")).getByText("N/A"),
    ).toBeInTheDocument();
    expect(
      within(await findPanel("UE connections")).getByText("N/A"),
    ).toBeInTheDocument();
    expect(
      within(await findPanel("Core nodes and UE connections")).getByText(
        "No operators.",
      ),
    ).toBeInTheDocument();
  });

  it("says when the metrics fail to load", async () => {
    serve(() => new Response("boom", { status: 500 }));
    renderDashboard();

    const memory = await findPanel("Process Memory");
    expect(
      await within(memory).findByText("Failed to load data."),
    ).toBeInTheDocument();
  });
});

describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [1023, "1,023 B"],
    [1536, "1.5 KiB"],
    [64 * 1024 * 1024, "64 MiB"],
    [3 * 1024 ** 5, "3,072 TiB"],
  ])("formats %d as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });
});
