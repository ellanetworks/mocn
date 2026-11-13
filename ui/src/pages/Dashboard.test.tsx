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
# TYPE ellamocn_radios_connected gauge
ellamocn_radios_connected 2
# TYPE ellamocn_s1_associations gauge
ellamocn_s1_associations{side="mme",state="connected"} 1
ellamocn_s1_associations{side="mme",state="disconnected"} 1
ellamocn_s1_associations{side="mme",state="setup_failed"} 0
ellamocn_s1_associations{side="mme",state="up"} 3
ellamocn_s1_associations{side="ran",state="connected"} 4
ellamocn_s1_associations{side="ran",state="up"} 2
`;

afterEach(() => {
  vi.unstubAllGlobals();
});

const serve = (metrics: () => Response) =>
  vi.stubGlobal("fetch", vi.fn(stubApi({ "/api/v1/metrics": metrics })));

const renderDashboard = () =>
  renderWithClient(
    <MemoryRouter>
      <Dashboard />
    </MemoryRouter>,
  );

const findPanel = async (title: string) =>
  (await screen.findByRole("heading", { name: title })).closest(
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

  it("shows the radio and operator figures from the metrics", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    const radios = await screen.findByRole("region", { name: "Radios" });
    expect(await within(radios).findByText("2")).toBeVisible();
    const operators = screen.getByRole("region", { name: "Operators" });
    expect(within(operators).getByText("3 / 5")).toBeVisible();
  });

  it("orders the rows radios, operators, system", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    await screen.findByRole("region", { name: "Radios" });
    expect(
      screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent),
    ).toEqual(["Radios", "Operators", "System"]);
  });

  it("explains the operator figures on hover", async () => {
    serve(() => new Response(METRICS, { status: 200 }));
    renderDashboard();

    fireEvent.mouseOver(await findPanel("MMEs Up"));
    expect(
      await screen.findByRole("tooltip", {
        name: "MMEs that accepted the S1 Setup of Ella MOCN, out of every MME configured across the operators.",
      }),
    ).toBeInTheDocument();
  });

  it("shows N/A for a metric the MOCN does not expose", async () => {
    serve(() => new Response("", { status: 200 }));
    renderDashboard();

    const database = await findPanel("Database Size");
    expect(await within(database).findByText("N/A")).toBeInTheDocument();
    expect(
      within(await findPanel("Connected")).getByText("N/A"),
    ).toBeInTheDocument();
    expect(
      within(await findPanel("MMEs Up")).getByText("N/A"),
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
