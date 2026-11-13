import { expect, test, type APIRequestContext } from "@playwright/test";
import { assertNoA11yViolations } from "../a11y";
import { adminToken, json, send } from "../api";

const OPERATOR = "E2E NG Operator";
const UNREACHABLE_AMF = "192.0.2.2";
const operatorPath = `/api/v1/operators/${encodeURIComponent(OPERATOR)}`;

const removeOperator = async (request: APIRequestContext, token: string) => {
  const response = await send(request, "delete", operatorPath, { token });
  if (!response.ok() && response.status() !== 404) {
    throw new Error(
      `could not delete ${OPERATOR}: ${response.status()} ${await response.text()}`,
    );
  }
};

test.describe("AMF status", () => {
  test.beforeEach(async ({ request }) => {
    const token = await adminToken(request);
    await removeOperator(request, token);
    await json(request, "post", "/api/v1/operators", {
      token,
      data: {
        name: OPERATOR,
        pws_operator: false,
        plmns: [{ mcc: "901", mnc: "76" }],
        global_enb_id: null,
        global_gnb_id: {
          plmn: { mcc: "901", mnc: "76" },
          gnb_id: 1,
          gnb_id_length: 22,
        },
        mmes: [],
        amfs: [{ tnl_addresses: [UNREACHABLE_AMF] }],
      },
    });
  });

  test.afterEach(async ({ request }) => {
    await removeOperator(request, await adminToken(request));
  });

  test("the operator page shows the NG status of its AMF", async ({
    page,
    request,
  }) => {
    await page.goto(`/operators/${encodeURIComponent(OPERATOR)}`);

    const nodes = page.getByRole("grid", { name: /^Core network nodes/ });
    const row = nodes.getByRole("row").filter({
      has: page.getByRole("gridcell", { name: UNREACHABLE_AMF, exact: true }),
    });
    await expect(
      row.locator(".MuiChip-root").filter({
        hasText: /^(Disconnected|Connected|Up|Setup failed)$/,
      }),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      row.getByRole("gridcell", { name: "AMF (5G)", exact: true }),
    ).toBeVisible();
    await expect(
      row.getByRole("gridcell", { name: "None", exact: true }),
    ).toBeVisible();
    await assertNoA11yViolations(page, "Operator AMF status");

    const token = await adminToken(request);
    const operator = await json<{
      ue_connections: number;
      amfs: {
        tnl_addresses: string[];
        status?: { state: string; ue_connections: number };
      }[];
    }>(request, "get", operatorPath, { token });
    expect(operator.amfs[0].tnl_addresses).toEqual([UNREACHABLE_AMF]);
    expect(operator.amfs[0].status?.state).toMatch(
      /^(disconnected|connected|up|setup_failed)$/,
    );
    expect(operator.amfs[0].status?.ue_connections).toBe(0);
    expect(operator.amfs[0].status).not.toHaveProperty("overload");
    expect(operator.ue_connections).toBe(0);
  });
});
