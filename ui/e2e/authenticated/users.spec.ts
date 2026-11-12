import { expect, test } from "@playwright/test";
import {
  ADMIN_EMAIL,
  adminToken,
  deleteUserIfPresent,
  RoleID,
  json,
  send,
  userPath,
} from "../api";

const EMAIL = "e2e-created@ellanetworks.com";
const PASSWORD = "E2eCreatedPassw0rd!";
const NEW_PASSWORD = "E2eChangedPassw0rd!";

test.beforeEach(async ({ request }) => {
  await deleteUserIfPresent(request, await adminToken(request), EMAIL);
});

test("an admin creates a user, changes its role and password, and deletes it", async ({
  page,
  request,
  playwright,
  baseURL,
}) => {
  const token = await adminToken(request);

  await page.goto("/users");
  await page.getByRole("button", { name: "Create" }).click();
  let dialog = page.getByRole("dialog", { name: "Create User" });
  await dialog.getByLabel(/^Email/).fill(EMAIL);
  await dialog.getByLabel(/^Password/).fill(PASSWORD);
  await dialog.getByRole("combobox", { name: /Role/ }).click();
  await page.getByRole("option", { name: /^Network Manager/ }).click();
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(page.getByText("User created successfully.")).toBeVisible();
  await expect(dialog).toBeHidden();

  expect(
    await json<{ role_id: number }>(request, "get", userPath(EMAIL), {
      token,
    }),
  ).toMatchObject({ role_id: RoleID.NetworkManager });

  await page.getByRole("link", { name: EMAIL }).click();
  await expect(page).toHaveURL(
    new RegExp(`/users/${encodeURIComponent(EMAIL)}$`),
  );
  await expect(
    page.getByRole("heading", { level: 1, name: new RegExp(`${EMAIL}$`) }),
  ).toBeVisible();
  await expect(
    page.getByText("Network Manager", { exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Edit role" }).click();
  dialog = page.getByRole("dialog", { name: "Edit Role" });
  await dialog.getByRole("combobox", { name: /Role/ }).click();
  await page.getByRole("option", { name: /^Read Only/ }).click();
  await dialog.getByRole("button", { name: "Update" }).click();
  await expect(page.getByText("User updated successfully.")).toBeVisible();
  await expect(page.getByText("Read Only", { exact: true })).toBeVisible();

  await page.getByRole("button", { name: "Change Password" }).click();
  dialog = page.getByRole("dialog", { name: "Change Password" });
  await expect(dialog.getByLabel(/^Current password/)).toHaveCount(0);
  await dialog.getByLabel(/^New password/).fill(NEW_PASSWORD);
  await dialog.getByRole("button", { name: "Update" }).click();
  await expect(page.getByText("Password updated successfully.")).toBeVisible();

  const context = await playwright.request.newContext({ baseURL });
  expect(
    (
      await send(context, "post", "/api/v1/auth/login", {
        data: { email: EMAIL, password: PASSWORD },
      })
    ).status(),
  ).toBe(401);
  expect(
    (
      await send(context, "post", "/api/v1/auth/login", {
        data: { email: EMAIL, password: NEW_PASSWORD },
      })
    ).ok(),
  ).toBe(true);
  await context.dispose();

  await page.getByRole("button", { name: "Delete" }).click();
  dialog = page.getByRole("dialog", { name: "Delete User" });
  await dialog.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText("User deleted successfully.")).toBeVisible();
  await expect(page).toHaveURL(/\/users$/);
  await expect(page.getByRole("link", { name: EMAIL })).toHaveCount(0);

  expect(
    (await send(request, "get", userPath(EMAIL), { token })).status(),
  ).toBe(404);
});

test("an admin cannot delete itself or change its own role", async ({
  page,
}) => {
  await page.goto(`/users/${encodeURIComponent(ADMIN_EMAIL)}`);
  await expect(page.getByText("Admin", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Edit role" })).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Change Password" }),
  ).toBeDisabled();
});
