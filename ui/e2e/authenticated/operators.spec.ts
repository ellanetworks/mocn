import { expect, test } from "@playwright/test";
import { adminToken, json, send } from "../api";
import { assertNoA11yViolations } from "../a11y";
import { ROLES } from "../roles";

const NAME = "E2E Operator";
const RENAMED = "E2E Operator Renamed";
const READ_ONLY_NAME = "E2E Read Only Operator";

const operatorPath = (name: string) =>
  `/api/v1/operators/${encodeURIComponent(name)}`;

const detailPath = (name: string) =>
  new RegExp(`/operators/${encodeURIComponent(name)}$`);

const heading = (name: string) => new RegExp(`^Operators\\s*/\\s*${name}$`);

const removeOperator = async (
  request: Parameters<typeof adminToken>[0],
  token: string,
  name: string,
) => {
  const response = await send(request, "delete", operatorPath(name), {
    token,
  });
  if (!response.ok() && response.status() !== 404) {
    throw new Error(
      `could not delete ${name}: ${response.status()} ${await response.text()}`,
    );
  }
};

test.describe("Admin", () => {
  test.beforeEach(async ({ request }) => {
    const token = await adminToken(request);
    await removeOperator(request, token, NAME);
    await removeOperator(request, token, RENAMED);
  });

  test("an admin creates an operator and changes it piece by piece", async ({
    page,
    request,
  }) => {
    const token = await adminToken(request);
    const saved = async (message: string) => {
      await expect(page.getByText(message)).toBeVisible();
      await expect(page.getByRole("dialog")).toHaveCount(0);
    };
    const lte = page.getByRole("table", { name: "Overview" });
    const nr = page.getByRole("table", { name: "Overview" });
    const mmes = page.getByRole("grid", { name: /^4G MMEs/ });
    const amfs = page.getByRole("grid", { name: /^5G AMFs/ });
    const overview = page.getByRole("table", { name: "Overview" });

    await page.goto("/operators");
    await page.getByRole("button", { name: "Create Operator" }).click();
    let dialog = page.getByRole("dialog", { name: "Create Operator" });
    await expect(dialog.getByRole("textbox")).toHaveCount(3);
    await dialog.getByRole("textbox", { name: "Name" }).fill(NAME);
    await dialog.getByRole("textbox", { name: "MCC" }).fill("901");
    await dialog.getByRole("textbox", { name: "MNC" }).fill("71");
    await dialog.getByRole("button", { name: "Create" }).click();
    await saved("Operator created successfully.");

    await expect(page).toHaveURL(detailPath(NAME));
    await expect(
      page.getByRole("heading", { level: 1, name: heading(NAME) }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Edit", exact: true }),
    ).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Add MME" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Add AMF" })).toBeDisabled();
    await expect(mmes).toContainText("No MMEs.");

    await lte
      .getByRole("button", {
        name: "Edit Gateway identity, 4G (Global eNB ID)",
      })
      .click();
    dialog = page.getByRole("dialog", { name: "Set Global eNB ID" });
    await dialog.getByRole("textbox", { name: "eNB ID" }).fill("703710");
    await dialog.getByRole("button", { name: "Save" }).click();
    await saved("Global eNB ID saved successfully.");
    await expect(
      lte.getByText("901/71 · Macro (20 bits) · 703710"),
    ).toBeVisible();

    await page.getByRole("button", { name: "Add MME" }).click();
    dialog = page.getByRole("dialog", { name: "Add MME" });
    await dialog.getByRole("textbox", { name: "Address" }).fill("10.0.0.1");
    await dialog.getByRole("button", { name: "Add" }).click();
    await saved("MME added successfully.");
    await expect(
      page.getByRole("heading", { level: 2, name: "4G MMEs (1)" }),
    ).toBeVisible();
    await expect(
      mmes.getByRole("gridcell", { name: "10.0.0.1", exact: true }),
    ).toBeVisible();
    await expect(
      lte.getByRole("button", {
        name: "Remove Gateway identity, 4G (Global eNB ID)",
      }),
    ).toBeDisabled();

    await mmes.getByRole("button", { name: "Remove MME 10.0.0.1" }).click();
    dialog = page.getByRole("dialog", { name: "Remove MME" });
    await dialog.getByRole("button", { name: "Remove" }).click();
    await saved("MME removed successfully.");
    await expect(mmes).toContainText("No MMEs.");

    await nr
      .getByRole("button", {
        name: "Edit Gateway identity, 5G (Global gNB ID)",
      })
      .click();
    dialog = page.getByRole("dialog", { name: "Set Global gNB ID" });
    await dialog
      .getByRole("textbox", { name: "gNB ID", exact: true })
      .fill("74565");
    await dialog.getByRole("button", { name: "Save" }).click();
    await saved("Global gNB ID saved successfully.");
    await expect(nr.getByText("901/71 · 74565 (22 bits)")).toBeVisible();

    await page.getByRole("button", { name: "Add AMF" }).click();
    dialog = page.getByRole("dialog", { name: "Add AMF" });
    await dialog.getByRole("textbox", { name: "Address" }).fill("10.0.1.1");
    await dialog.getByRole("button", { name: "Add" }).click();
    await saved("AMF added successfully.");
    await expect(
      amfs.getByRole("gridcell", { name: "10.0.1.1", exact: true }),
    ).toBeVisible();

    const pwsRow = overview
      .getByRole("row")
      .filter({ hasText: "PWS operator" });
    await expect(pwsRow).toContainText("No");
    await expect(page.getByRole("switch")).toHaveCount(0);
    await overview.getByRole("button", { name: "Edit PWS operator" }).click();
    dialog = page.getByRole("dialog", { name: "Edit PWS Operator" });
    await assertNoA11yViolations(page, "Edit PWS Operator dialog");
    const pws = dialog.getByRole("switch", { name: "PWS operator" });
    await expect(pws).not.toBeChecked();
    await pws.click();
    await dialog.getByRole("button", { name: "Update" }).click();
    await saved("PWS operator updated successfully.");
    await expect(pwsRow).toContainText("Yes");

    expect(
      await json(request, "get", operatorPath(NAME), { token }),
    ).toMatchObject({
      name: NAME,
      pws_operator: true,
      plmns: [{ mcc: "901", mnc: "71" }],
      global_enb_id: {
        plmn: { mcc: "901", mnc: "71" },
        enb_id_type: "macro",
        enb_id: 703710,
      },
      global_gnb_id: {
        plmn: { mcc: "901", mnc: "71" },
        gnb_id: 74565,
        gnb_id_length: 22,
      },
      mmes: [],
      amfs: ["10.0.1.1"],
    });

    await page.getByRole("button", { name: "Edit Name" }).click();
    dialog = page.getByRole("dialog", { name: "Rename Operator" });
    await dialog.getByRole("textbox", { name: "Name" }).fill(RENAMED);
    await dialog.getByRole("button", { name: "Rename" }).click();
    await saved("Operator renamed successfully.");
    await expect(page).toHaveURL(detailPath(RENAMED));
    await expect(
      page.getByRole("heading", { level: 1, name: heading(RENAMED) }),
    ).toBeVisible();
    await expect(
      amfs.getByRole("gridcell", { name: "10.0.1.1", exact: true }),
    ).toBeVisible();
    expect(
      (await send(request, "get", operatorPath(NAME), { token })).status(),
    ).toBe(404);

    await page.getByRole("button", { name: "Delete" }).click();
    dialog = page.getByRole("dialog", { name: "Delete Operator" });
    await dialog.getByRole("button", { name: "Delete" }).click();
    await expect(
      page.getByText("Operator deleted successfully."),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/operators$/);
    await expect(
      page.getByRole("row").filter({ hasText: RENAMED }),
    ).toHaveCount(0);
    expect(
      (await send(request, "get", operatorPath(RENAMED), { token })).status(),
    ).toBe(404);
  });
});

test.describe("Read Only", () => {
  test.use({ storageState: ROLES["Read Only"].session });

  test.beforeEach(async ({ request }) => {
    const token = await adminToken(request);
    await removeOperator(request, token, READ_ONLY_NAME);
    await json(request, "post", "/api/v1/operators", {
      token,
      data: {
        name: READ_ONLY_NAME,
        pws_operator: false,
        plmns: [{ mcc: "901", mnc: "72" }],
        mmes: [],
        amfs: [],
      },
    });
  });

  test.afterEach(async ({ request }) => {
    await removeOperator(request, await adminToken(request), READ_ONLY_NAME);
  });

  test("a read-only user sees the operators but cannot change them", async ({
    page,
  }) => {
    await page.goto("/operators");

    const row = page.getByRole("row").filter({ hasText: READ_ONLY_NAME });
    await expect(row).toContainText("901/72");
    await expect(
      page.getByRole("button", { name: "Create Operator" }),
    ).toHaveCount(0);

    await row.getByRole("link", { name: READ_ONLY_NAME }).click();
    await expect(page).toHaveURL(detailPath(READ_ONLY_NAME));
    await expect(
      page.getByRole("heading", { level: 1, name: heading(READ_ONLY_NAME) }),
    ).toBeVisible();
    await expect(page.getByText("Not set")).toHaveCount(2);
    await expect(page.getByRole("main").getByRole("button")).toHaveCount(0);
    await expect(page.getByRole("switch")).toHaveCount(0);
  });
});
