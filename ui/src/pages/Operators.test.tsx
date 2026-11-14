import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { json, readOnlyAuth } from "@/test/render";
import {
  operatorB,
  operatorWithStatus,
  serve,
  renderAt,
  location,
  fill,
  blur,
  cells,
  rowOf,
  saved,
} from "@/test/operators";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("Operators", () => {
  it("lists the operators, each a link to its page", async () => {
    serve({
      operators: [
        { ...operatorWithStatus(), ue_connections: 3 },
        { ...operatorB(), ue_connections: 0 },
      ],
    });

    renderAt("/operators", readOnlyAuth);

    expect(
      await screen.findByRole("heading", { level: 1, name: "Operators (2)" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual(["Name", "PLMNs", "UE Connections", "PWS", "Core Network Nodes"]);
    expect(cells(await rowOf("Operator A"))).toEqual([
      "Operator A",
      "001-01, 001-02",
      "3",
      "PWS",
      "2/5 up",
    ]);
    expect(cells(await rowOf("Operator B"))).toEqual([
      "Operator B",
      "315-010",
      "0",
      "",
      "None",
    ]);
    expect(screen.getByRole("link", { name: "Operator A" })).toHaveAttribute(
      "href",
      "/operators/Operator%20A",
    );
    expect(
      screen.queryByRole("button", { name: /^(Edit|Delete) / }),
    ).not.toBeInTheDocument();
  });

  it("says what the UE connections count on hover", async () => {
    serve();

    renderAt("/operators");
    await rowOf("Operator A");

    fireEvent.mouseOver(screen.getByText("UE Connections"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Devices connected to the operator's core: one per active phone, over 4G (S1) or 5G (NG).",
      }),
    ).toBeInTheDocument();
  });

  it("deletes an operator from the list after confirming", async () => {
    const requests = serve();

    renderAt("/operators");
    fireEvent.click(
      await screen.findByRole("button", { name: "Delete Operator B" }),
    );
    const dialog = screen.getByRole("dialog", { name: "Delete Operator" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    expect(
      await screen.findByText("Operator deleted successfully."),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByText("Operator B")).not.toBeInTheDocument(),
    );
    expect(location()).toBe("/operators");
    expect(requests).toEqual([
      { method: "DELETE", path: "/api/v1/operators/Operator B" },
    ]);
  });

  it("opens an operator from its name", async () => {
    serve();

    renderAt("/operators");
    fireEvent.click(await screen.findByRole("link", { name: "Operator B" }));

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Operators\s*\/\s*Operator B$/,
      }),
    ).toBeInTheDocument();
    expect(location()).toBe("/operators/Operator%20B");
  });

  it("shows an empty state", async () => {
    serve({ operators: [] });

    renderAt("/operators");

    expect(await screen.findByText("No operators.")).toBeInTheDocument();
  });

  it("shows no grid when the operators fail to load", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => json(500, { error: "boom" })),
    );

    renderAt("/operators");

    expect(
      await screen.findByText("Failed to load operators"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("grid", { name: "Operators" }),
    ).not.toBeInTheDocument();
  });

  it("offers no Create to a Read Only user", async () => {
    serve();

    renderAt("/operators", readOnlyAuth);

    expect(cells(await rowOf("Operator A"))).toHaveLength(5);
    expect(
      screen.queryByRole("button", { name: "Create" }),
    ).not.toBeInTheDocument();
  });

  it("creates an operator from a name and PLMNs, then opens it", async () => {
    const requests = serve({ operators: [] });

    renderAt("/operators");
    await screen.findByText("No operators.");
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    const dialog = screen.getByRole("dialog", { name: "Create Operator" });
    expect(within(dialog).getAllByRole("textbox")).toHaveLength(3);
    for (const name of ["Name", "MCC", "MNC"]) {
      expect(within(dialog).getByRole("textbox", { name })).toBeInTheDocument();
    }
    expect(within(dialog).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(within(dialog).queryByRole("combobox")).not.toBeInTheDocument();
    const create = within(dialog).getByRole("button", { name: "Create" });
    expect(create).toBeDisabled();
    expect(
      within(dialog).getByRole("button", { name: "Remove PLMN 1" }),
    ).toBeDisabled();

    fill("Name", "x".repeat(65));
    expect(
      within(dialog).queryByText("At most 64 characters"),
    ).not.toBeInTheDocument();
    blur("Name");
    expect(
      within(dialog).getByText("At most 64 characters"),
    ).toBeInTheDocument();
    fill("Name", "");
    expect(within(dialog).getByText("Required")).toBeInTheDocument();
    fill("Name", "Operator C");
    fill("MCC", "00");
    expect(within(dialog).queryByText("3 digits")).not.toBeInTheDocument();
    blur("MCC");
    expect(within(dialog).getByText("3 digits")).toBeInTheDocument();
    fill("MCC", "001");
    fill("MNC", "01");
    expect(create).toBeEnabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Add PLMN" }));
    expect(create).toBeDisabled();
    fill("MCC", "001", 1);
    fill("MNC", "01", 1);
    expect(create).toBeDisabled();
    expect(
      within(dialog).queryByText("Duplicate PLMN"),
    ).not.toBeInTheDocument();
    blur("MNC", 1);
    expect(within(dialog).getByText("Duplicate PLMN")).toBeInTheDocument();
    fill("MNC", "02", 1);
    expect(create).toBeEnabled();
    fireEvent.click(create);

    await saved("Operator created successfully.");
    expect(requests).toEqual([
      {
        method: "POST",
        path: "/api/v1/operators",
        body: {
          name: "Operator C",
          pws_operator: false,
          plmns: [
            { mcc: "001", mnc: "01" },
            { mcc: "001", mnc: "02" },
          ],
          mmes: [],
          amfs: [],
        },
      },
    ]);
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Operators\s*\/\s*Operator C$/,
      }),
    ).toBeInTheDocument();
    expect(location()).toBe("/operators/Operator%20C");
  });

  it("shows the server's error in the dialog", async () => {
    serve({
      operators: [],
      answer: () =>
        json(409, { error: "Another operator already has PLMN 00101" }),
    });

    renderAt("/operators");
    await screen.findByText("No operators.");
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    fill("Name", "Operator C");
    fill("MCC", "001");
    fill("MNC", "01");
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Create",
      }),
    );

    expect(
      await screen.findByText(
        "Could not create the operator: Another operator already has PLMN 00101",
      ),
    ).toBeInTheDocument();
  });
});
