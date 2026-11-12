import { expect, test } from "@playwright/test";
import {
  adminToken,
  deleteUserIfPresent,
  ensureUser,
  RoleID,
  send,
} from "../api";

const EMAIL = "e2e-profile@ellanetworks.com";
const PASSWORD = "E2eProfilePassw0rd!";
const NEW_PASSWORD = "E2eProfileChanged0!";
const TOKEN_NAME = "E2E monitoring";

test.describe.configure({ mode: "serial" });

test.beforeAll(async ({ request }) => {
  const token = await adminToken(request);
  await deleteUserIfPresent(request, token, EMAIL);
  await ensureUser(request, token, EMAIL, PASSWORD, RoleID.NetworkManager);
});

test.afterAll(async ({ request }) => {
  await deleteUserIfPresent(request, await adminToken(request), EMAIL);
});

test.use({ storageState: { cookies: [], origins: [] } });

test("a user creates an API token, uses it, and deletes it", async ({
  page,
  request,
}) => {
  await page.goto("/login");
  await page.getByLabel(/^Email/).fill(EMAIL);
  await page.getByLabel(/^Password/).fill(PASSWORD);
  await page.getByRole("button", { name: "Login" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto("/profile");

  await page.getByRole("button", { name: "Create Token" }).click();
  const dialog = page.getByRole("dialog", { name: "Create API Token" });
  await dialog.getByLabel(/^Name/).fill(TOKEN_NAME);
  await dialog.getByRole("checkbox", { name: "No expiry" }).check();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(dialog).toBeHidden();

  const secret = (await page.getByTestId("new-api-token").innerText()).trim();
  expect(secret).not.toBe("");
  const table = page.getByRole("table", { name: "API tokens" });
  await expect(table.getByText(TOKEN_NAME)).toBeVisible();

  const me = await send(request, "get", "/api/v1/users/me", { token: secret });
  expect(me.ok()).toBe(true);
  expect(
    ((await me.json()) as { result: { email: string } }).result.email,
  ).toBe(EMAIL);

  await page.getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByTestId("new-api-token")).toHaveCount(0);

  await page.getByRole("button", { name: `Delete ${TOKEN_NAME}` }).click();
  await page
    .getByRole("dialog", { name: "Delete API Token" })
    .getByRole("button", { name: "Delete" })
    .click();
  await expect(
    page.getByText(`API token "${TOKEN_NAME}" deleted successfully.`),
  ).toBeVisible();
  await expect(table.getByText(TOKEN_NAME)).toHaveCount(0);

  expect(
    (
      await send(request, "get", "/api/v1/users/me", { token: secret })
    ).status(),
  ).toBe(401);
});

test("a user changes its password, logs out, and logs in with the new one", async ({
  page,
}) => {
  await page.goto("/login");
  await page.getByLabel(/^Email/).fill(EMAIL);
  await page.getByLabel(/^Password/).fill(PASSWORD);
  await page.getByRole("button", { name: "Login" }).click();
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto("/profile");

  await page.getByRole("button", { name: "Change Password" }).click();
  const dialog = page.getByRole("dialog", { name: "Change Password" });
  await dialog.getByLabel(/^Current password/).fill(PASSWORD);
  await dialog.getByLabel(/^New password/).fill(NEW_PASSWORD);
  await dialog.getByRole("button", { name: "Update" }).click();
  await expect(page.getByText("Password updated successfully.")).toBeVisible();

  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Log Out" }).click();
  await expect(page).toHaveURL(/\/login$/);

  await page.goto("/profile");
  await expect(page).toHaveURL(/\/login$/);

  await page.getByLabel(/^Email/).fill(EMAIL);
  await page.getByLabel(/^Password/).fill(NEW_PASSWORD);
  await page.getByRole("button", { name: "Login" }).click();
  await expect(page).toHaveURL(/\/profile$/);
  await expect(page.getByText(EMAIL, { exact: true }).first()).toBeVisible();
});
