import { expect, test } from "@playwright/test";
import { assertNoA11yViolations } from "../a11y";
import { ADMIN_EMAIL, ADMIN_PASSWORD, isInitialized } from "../api";

test("a new MOCN creates its first admin, who lands on the Dashboard", async ({
  page,
  request,
}) => {
  test.skip(await isInitialized(request), "The MOCN is already initialized.");

  await page.goto("/");
  await expect(page).toHaveURL(/\/initialize$/);
  await expect(
    page.getByRole("heading", { name: "Initialize Ella MOCN" }),
  ).toBeVisible();
  await assertNoA11yViolations(page, "Initialize");

  await page.getByLabel(/^Email/).fill(ADMIN_EMAIL);
  await page.getByLabel(/^Password/).fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Create" }).click();

  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(
    page.getByRole("heading", { level: 1, name: "Dashboard" }),
  ).toBeVisible();

  await page.goto("/users");
  await expect(
    page.getByRole("heading", { level: 1, name: "Users (1)" }),
  ).toBeVisible();
  await expect(page.getByRole("link", { name: ADMIN_EMAIL })).toBeVisible();
  expect(await isInitialized(request)).toBe(true);
});
