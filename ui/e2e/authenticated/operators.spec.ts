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

    const identityRow = overview
      .getByRole("row")
      .filter({ hasText: "Gateway identity" })
      .first();
    await overview.getByText("Gateway identity", { exact: true }).hover();
    await expect(
      page.getByRole("tooltip", {
        name: "Assigned by the operator. Ella MOCN uses it to identify itself to this operator's core: the Global eNB ID to its MMEs (4G) and the Global gNB ID to its AMFs (5G).",
      }),
    ).toBeVisible();
    await expect(identityRow.getByText("Not set")).toHaveCount(2);

    const editIdentity = async () => {
      await overview
        .getByRole("button", { name: "Edit Gateway identity" })
        .click();
      const identity = page.getByRole("dialog", {
        name: "Edit Gateway Identity",
      });
      return {
        identity,
        lte: identity.getByRole("group", { name: "4G (Global eNB ID)" }),
        nr: identity.getByRole("group", { name: "5G (Global gNB ID)" }),
      };
    };

    let { identity, lte, nr } = await editIdentity();
    await lte.getByRole("switch", { name: "Set" }).click();
    await lte.getByRole("textbox", { name: "eNB ID" }).fill("1048576");
    await lte.getByRole("textbox", { name: "eNB ID" }).blur();
    await expect(lte.getByText("0 to 1048575")).toBeVisible();
    await expect(identity.getByRole("button", { name: "Save" })).toBeDisabled();
    await lte.getByRole("textbox", { name: "eNB ID" }).fill("703710");
    await nr.getByRole("switch", { name: "Set" }).click();
    await nr
      .getByRole("textbox", { name: "gNB ID", exact: true })
      .fill("74565");
    await assertNoA11yViolations(page, "Edit Gateway Identity dialog");
    await identity.getByRole("button", { name: "Save" }).click();
    await saved("Gateway identity saved successfully.");
    await expect(
      identityRow.getByText("901/71 · Macro (20 bits) · 703710"),
    ).toBeVisible();
    await expect(
      identityRow.getByText("901/71 · 74565 (22 bits)"),
    ).toBeVisible();

    await page.getByRole("button", { name: "Add MME" }).click();
    dialog = page.getByRole("dialog", { name: "Add MME" });
    await dialog.getByRole("textbox", { name: "Address 1" }).fill("10.0.0.1");
    await dialog.getByRole("button", { name: "Add address" }).click();
    await dialog.getByRole("textbox", { name: "Address 2" }).fill("10.0.0.1");
    await dialog.getByRole("textbox", { name: "Address 2" }).blur();
    await expect(dialog.getByText("Duplicate address")).toBeVisible();
    await expect(
      dialog.getByRole("button", { name: "Add", exact: true }),
    ).toBeDisabled();
    await dialog.getByRole("textbox", { name: "Address 2" }).fill("10.0.0.2");
    await assertNoA11yViolations(page, "Add MME dialog");
    await dialog.getByRole("button", { name: "Add", exact: true }).click();
    await saved("MME added successfully.");
    await expect(
      page.getByRole("heading", { level: 2, name: "4G MMEs (1)" }),
    ).toBeVisible();
    await expect(
      mmes.getByRole("gridcell", { name: "10.0.0.1, 10.0.0.2", exact: true }),
    ).toBeVisible();
    ({ identity, lte, nr } = await editIdentity());
    await expect(lte.getByRole("switch", { name: "Set" })).toBeDisabled();
    await expect(lte.getByText("Remove the MMEs first")).toBeVisible();
    await nr.getByRole("switch", { name: "Set" }).click();
    await identity.getByRole("button", { name: "Save" }).click();
    await saved("Gateway identity saved successfully.");
    await expect(identityRow.getByText("Not set")).toHaveCount(1);

    await mmes
      .getByRole("button", { name: "Remove MME 10.0.0.1, 10.0.0.2" })
      .click();
    dialog = page.getByRole("dialog", { name: "Remove MME" });
    await dialog.getByRole("button", { name: "Remove" }).click();
    await saved("MME removed successfully.");
    await expect(mmes).toContainText("No MMEs.");

    ({ identity, nr } = await editIdentity());
    await nr.getByRole("switch", { name: "Set" }).click();
    await nr
      .getByRole("textbox", { name: "gNB ID", exact: true })
      .fill("74565");
    await identity.getByRole("button", { name: "Save" }).click();
    await saved("Gateway identity saved successfully.");
    await expect(
      identityRow.getByText("901/71 · 74565 (22 bits)"),
    ).toBeVisible();

    await page.getByRole("button", { name: "Add AMF" }).click();
    dialog = page.getByRole("dialog", { name: "Add AMF" });
    await dialog.getByRole("textbox", { name: "Address 1" }).fill("10.0.1.1");
    await dialog.getByRole("button", { name: "Add", exact: true }).click();
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
      amfs: [{ tnl_addresses: ["10.0.1.1"] }],
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
    await expect(
      page.getByRole("button", { name: "Edit Gateway identity" }),
    ).toHaveCount(0);
    await expect(page.getByRole("switch")).toHaveCount(0);
  });
});
