import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import App from "@/App";
import { RoleID } from "@/queries/users";
import { page } from "@/test/fixtures";
import { makeToken } from "@/test/jwt";
import { json, renderWithClient, stubApi } from "@/test/render";

const serve = (role: RoleID | null = RoleID.Admin) => {
  let session = role;
  vi.stubGlobal(
    "fetch",
    vi.fn(
      stubApi({
        "/api/v1/auth/refresh": () =>
          session === null
            ? json(401, { error: "No valid session" })
            : json(200, {
                result: { token: makeToken("user@example.com", session) },
              }),
        "/api/v1/auth/logout": () => {
          session = null;
          return new Response(null, { status: 204 });
        },
        "/api/v1/status": () =>
          json(200, { result: { version: "v0.0.1", initialized: true } }),
        "/api/v1/users": () =>
          json(200, {
            result: page([{ email: "user@example.com", role_id: role }]),
          }),
        "/api/v1/users/me": () =>
          json(200, { result: { email: "user@example.com", role_id: role } }),
        "/api/v1/users/me/api-tokens": () => json(200, { result: page([]) }),
        "/api/v1/metrics": () => new Response("", { status: 200 }),
        "/api/v1/operators": () => json(200, { result: { items: [] } }),
        "/api/v1/ran-nodes": () => json(200, { result: { items: [] } }),
      }),
    ),
  );
};

const renderAt = (path: string) =>
  renderWithClient(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("App", () => {
  it("renders the top bar, the navigation and the footer", async () => {
    serve();
    renderAt("/users");

    expect(
      await screen.findByRole("img", { name: "Ella MOCN Logo" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("banner")).toHaveTextContent("Ella MOCN");
    expect(
      within(screen.getByRole("navigation", { name: "Main" }))
        .getAllByRole("link")
        .map((link) => [link.textContent, link.getAttribute("href")]),
    ).toEqual([
      ["Dashboard", "/dashboard"],
      ["Operators", "/operators"],
      ["RAN Nodes", "/ran-nodes"],
      ["Users", "/users"],
    ]);
    expect(screen.getByRole("main")).toBeInTheDocument();
    expect(screen.getByRole("contentinfo")).toBeInTheDocument();

    const docs = screen.getByRole("link", { name: "Documentation" });
    expect(docs).toHaveAttribute("href", "https://docs.ellanetworks.com/mocn/");
    expect(docs).toHaveAttribute("target", "_blank");

    const bug = screen.getByRole("link", { name: "Report a bug" });
    expect(bug).toHaveAttribute(
      "href",
      "https://github.com/ellanetworks/mocn/issues/new/choose",
    );
    expect(bug).toHaveAttribute("target", "_blank");
  });

  it("opens the Dashboard by default", async () => {
    serve();
    renderAt("/");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Dashboard" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Dashboard" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    expect(document.title).toBe("Dashboard · Ella MOCN");
  });

  it("goes to the login page without a session", async () => {
    serve(null);
    renderAt("/users");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Login" }),
    ).toBeInTheDocument();
  });

  it("shows a Read Only user the Dashboard and not Users", async () => {
    serve(RoleID.ReadOnly);
    renderAt("/users");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Dashboard" }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole("navigation", { name: "Main" }))
        .getAllByRole("link")
        .map((link) => link.textContent),
    ).toEqual(["Dashboard", "Operators", "RAN Nodes"]);
  });

  it("shows a Read Only user the Operators", async () => {
    serve(RoleID.ReadOnly);
    renderAt("/operators");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Operators (0)" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Operators" })).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it.each([RoleID.Admin, RoleID.NetworkManager, RoleID.ReadOnly])(
    "shows role %i the RAN Nodes",
    async (role) => {
      serve(role);
      renderAt("/ran-nodes");

      expect(
        await screen.findByRole("heading", { level: 1, name: "RAN Nodes (0)" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("link", { name: "RAN Nodes" })).toHaveAttribute(
        "aria-current",
        "page",
      );
    },
  );

  it("opens the profile from the account menu", async () => {
    serve();
    renderAt("/users");

    fireEvent.click(
      await screen.findByRole("button", { name: "Account menu" }),
    );
    fireEvent.click(screen.getByRole("menuitem", { name: "Profile" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Profile" }),
    ).toBeInTheDocument();
  });

  it("logs out from the account menu", async () => {
    serve();
    renderAt("/users");

    fireEvent.click(
      await screen.findByRole("button", { name: "Account menu" }),
    );
    expect(screen.getByText("user@example.com")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("menuitem", { name: "Log Out" }));

    expect(
      await screen.findByRole("heading", { level: 1, name: "Login" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        vi
          .mocked(fetch)
          .mock.calls.some(
            ([input]) => String(input) === "/api/v1/auth/logout",
          ),
      ).toBe(true),
    );
  });
});
