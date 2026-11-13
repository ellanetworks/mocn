import { expect, test, type APIRequestContext } from "@playwright/test";
import { assertNoA11yViolations } from "../a11y";
import { adminToken, json, send } from "../api";
import { ROLES } from "../roles";

const OPERATOR = "E2E S1 Operator";
const UNREACHABLE_MME = "192.0.2.1";
const operatorPath = `/api/v1/operators/${encodeURIComponent(OPERATOR)}`;

const removeOperator = async (request: APIRequestContext, token: string) => {
  const response = await send(request, "delete", operatorPath, { token });
  if (!response.ok() && response.status() !== 404) {
    throw new Error(
      `could not delete ${OPERATOR}: ${response.status()} ${await response.text()}`,
    );
  }
};

const SESSIONS = [
  { name: "Admin", session: undefined },
  ...Object.entries(ROLES).map(([name, role]) => ({
    name,
    session: role.session,
  })),
];

for (const { name, session } of SESSIONS) {
  test.describe(name, () => {
    if (session) test.use({ storageState: session });

    test("sees no radios while none is connected", async ({ page }) => {
      await page.goto("/radios");

      await expect(
        page.getByRole("heading", { level: 1, name: "Radios (0)" }),
      ).toBeVisible();
      const grid = page.getByRole("grid", { name: "Radios" });
      await expect(grid).toContainText("No radios.");
      await expect(grid.getByRole("columnheader")).toHaveText([
        "Name",
        "Type",
        "Global ID",
        "Addresses",
        "UE connections",
        "Status",
      ]);
      await expect(
        page
          .getByRole("navigation", { name: "Main" })
          .getByRole("link", { name: "Radios" }),
      ).toHaveAttribute("aria-current", "page");
      await assertNoA11yViolations(page, `Radios (${name})`);
    });
  });
}

test.describe("MME status", () => {
  test.beforeEach(async ({ request }) => {
    const token = await adminToken(request);
    await removeOperator(request, token);
    await json(request, "post", "/api/v1/operators", {
      token,
      data: {
        name: OPERATOR,
        pws_operator: false,
        plmns: [{ mcc: "901", mnc: "75" }],
        global_enb_id: {
          plmn: { mcc: "901", mnc: "75" },
          enb_id_type: "macro",
          enb_id: 1,
        },
        global_gnb_id: null,
        mmes: [{ tnl_addresses: [UNREACHABLE_MME] }],
        amfs: [],
      },
    });
  });

  test.afterEach(async ({ request }) => {
    await removeOperator(request, await adminToken(request));
  });

  test("the operator page shows the S1 status of its MME", async ({
    page,
    request,
  }) => {
    await page.goto(`/operators/${encodeURIComponent(OPERATOR)}`);

    const mmes = page.getByRole("grid", { name: /^Core network nodes/ });
    const row = mmes.getByRole("row").filter({
      has: page.getByRole("gridcell", { name: UNREACHABLE_MME, exact: true }),
    });
    await expect(
      row.locator(".MuiChip-root").filter({
        hasText: /^(Disconnected|Connected|Up|Setup failed)$/,
      }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(mmes.getByRole("columnheader")).toHaveText([
      "Addresses",
      "Type",
      "Name",
      "Served IDs",
      "Capacity",
      "UE connections",
      "Overload",
      "Status",
      "Actions",
    ]);
    await expect(
      row.getByRole("gridcell", { name: "None", exact: true }),
    ).toBeVisible();
    const connections = page.getByRole("grid", {
      name: "UE connections by radio",
    });
    await expect(connections).toContainText("No radios.");
    await expect(connections.getByRole("columnheader")).toHaveText([
      "Radio",
      "UE connections",
    ]);
    await assertNoA11yViolations(page, "Operator MME status");

    const token = await adminToken(request);
    const operator = await json<{
      ue_connections: number;
      ue_connections_by_radio: unknown[];
      mmes: {
        tnl_addresses: string[];
        status?: { state: string; ue_connections: number };
      }[];
    }>(request, "get", operatorPath, { token });
    expect(operator.mmes[0].tnl_addresses).toEqual([UNREACHABLE_MME]);
    expect(operator.mmes[0].status?.state).toMatch(
      /^(disconnected|connected|up|setup_failed)$/,
    );
    expect(operator.mmes[0].status?.ue_connections).toBe(0);
    expect(operator.mmes[0].status).not.toHaveProperty("overload");
    expect(operator.ue_connections).toBe(0);
    expect(operator.ue_connections_by_radio).toEqual([]);
    expect(
      await json<{ items: unknown[] }>(
        request,
        "get",
        `/api/v1/ue-connections?operator=${encodeURIComponent(OPERATOR)}`,
        { token },
      ),
    ).toEqual({ items: [] });
  });
});
