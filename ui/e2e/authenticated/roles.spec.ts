import { expect, test } from "@playwright/test";
import { ROLES } from "../roles";

for (const [name, role] of Object.entries(ROLES)) {
  test.describe(name, () => {
    test.use({ storageState: role.session });

    test("lands on the Dashboard, and cannot open Users", async ({ page }) => {
      await page.goto("/");

      await expect(page).toHaveURL(/\/dashboard$/);
      await expect(
        page.getByRole("heading", { level: 1, name: "Dashboard" }),
      ).toBeVisible();
      const links = page
        .getByRole("navigation", { name: "Main" })
        .getByRole("link");
      await expect(links).toHaveCount(1);
      await expect(links).toHaveText("Dashboard");

      await page.goto("/users");
      await expect(page).toHaveURL(/\/dashboard$/);

      await page.goto("/profile");
      await expect(page.getByText(name, { exact: true })).toBeVisible();
    });
  });
}
