import {
  expect,
  test,
  type APIRequestContext,
  type Page,
} from "@playwright/test";
import { assertNoA11yViolations } from "../a11y";
import { ADMIN_EMAIL, adminToken, json, send } from "../api";

const OPERATOR = "E2E Route Operator";
const operatorPath = `/api/v1/operators/${encodeURIComponent(OPERATOR)}`;

const removeOperator = async (request: APIRequestContext, token: string) => {
  const response = await send(request, "delete", operatorPath, { token });
  if (!response.ok() && response.status() !== 404) {
    throw new Error(
      `could not delete ${OPERATOR}: ${response.status()} ${await response.text()}`,
    );
  }
};

const createOperator = async (request: APIRequestContext) => {
  const token = await adminToken(request);
  await removeOperator(request, token);
  await json(request, "post", "/api/v1/operators", {
    token,
    data: {
      name: OPERATOR,
      pws_operator: true,
      plmns: [{ mcc: "901", mnc: "74" }],
      global_enb_id: {
        plmn: { mcc: "901", mnc: "74" },
        enb_id_type: "macro",
        enb_id: 703710,
      },
      global_gnb_id: {
        plmn: { mcc: "901", mnc: "74" },
        gnb_id: 74565,
        gnb_id_length: 22,
      },
      mmes: [{ tnl_addresses: ["10.0.0.1"] }],
      amfs: [{ tnl_addresses: ["10.0.1.1"] }],
    },
  });
  return () => removeOperator(request, token);
};

interface RouteCase {
  label: string;
  route: string;
  heading: string | RegExp;
  ready?: string;
  prepare?: (request: APIRequestContext) => Promise<() => Promise<void>>;
}

const ROUTES: RouteCase[] = [
  { label: "Dashboard", route: "/dashboard", heading: "Dashboard" },
  { label: "Operators", route: "/operators", heading: /^Operators/ },
  {
    label: "Operator",
    route: `/operators/${encodeURIComponent(OPERATOR)}`,
    heading: new RegExp(`^Operators\\s*/\\s*${OPERATOR}$`),
    ready: "Overview",
    prepare: createOperator,
  },
  { label: "Radios", route: "/radios", heading: /^Radios/ },
  { label: "Users", route: "/users", heading: /^Users/ },
  {
    label: "User",
    route: `/users/${encodeURIComponent(ADMIN_EMAIL)}`,
    heading: new RegExp(`^Users\\s*/\\s*${ADMIN_EMAIL.replace(/\./g, "\\.")}$`),
  },
  { label: "Profile", route: "/profile", heading: "Profile" },
];

const settle = async (page: Page, heading: string | RegExp) => {
  await expect(
    page.getByRole("heading", { level: 1, name: heading }),
  ).toBeVisible();
  await expect(page.getByRole("progressbar")).toHaveCount(0, {
    timeout: 15_000,
  });
};

for (const { label, route, heading, ready, prepare } of ROUTES) {
  test(`${label} renders`, async ({ page, request }) => {
    const cleanup = await prepare?.(request);
    const pageErrors: string[] = [];
    page.on("pageerror", (error) => pageErrors.push(String(error)));

    await page.goto(route);
    await settle(page, heading);
    if (ready) {
      await expect(
        page.getByRole("heading", { level: 2, name: ready }),
      ).toBeVisible();
    }

    const body = (await page.locator("body").innerText()).trim();
    for (const placeholder of [
      /\bundefined\b/,
      /\bNaN\b/,
      /\[object Object\]/,
    ]) {
      expect(body, `${route} rendered a placeholder value`).not.toMatch(
        placeholder,
      );
    }

    expect(pageErrors, `${route} raised an uncaught error`).toEqual([]);

    await assertNoA11yViolations(page, label);
    await cleanup?.();
  });
}
