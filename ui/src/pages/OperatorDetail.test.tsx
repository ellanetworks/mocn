import { afterEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { Operator } from "@/queries/operators";
import { RoleID } from "@/queries/users";
import { authFor } from "@/contexts/AuthContext";
import { json, readOnlyAuth } from "@/test/render";
import {
  operatorA,
  operatorWithStatus,
  operatorB,
  serve,
  renderAt,
  location,
  fill,
  blur,
  cells,
  NODES,
  nodeRow,
  mmeRow,
  click,
  submitButton,
  submit,
  put,
  saved,
  table,
  LTE,
  NR,
  toggle,
  choose,
  valueOf,
  identityOf,
  coreNodes,
  NODE_COLUMNS,
} from "@/test/operators";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("OperatorDetail", () => {
  it("shows every section of an operator", async () => {
    serve();

    renderAt("/operators/Operator%20A");

    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Operators\s*\/\s*Operator A$/,
      }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Operators" })).toHaveAttribute(
      "href",
      "/operators",
    );
    expect(document.title).toBe("Operators / Operator A · Ella MOCN");
    for (const [name, description] of [
      ["Overview", "How Ella MOCN identifies this operator."],
      ["Core Network Nodes (3)", "The MMEs and AMFs of this operator's core."],
    ]) {
      expect(
        await screen.findByRole("heading", { level: 2, name }),
      ).toBeInTheDocument();
      expect(screen.getByText(description)).toBeInTheDocument();
    }

    expect(valueOf("Overview", "Name")).toBe("Operator A");
    expect(table("Overview").getByTitle("001-01, 001-02")).toHaveTextContent(
      "001-01, 001-02",
    );
    expect(valueOf("Overview", "PWS Operator")).toBe("Yes");
    expect(identityOf(LTE)).toBe("001-01 · Macro (20 bits) · 703710");
    expect(identityOf(NR)).toBe("001-02 · 74565 (22 bits)");
    expect(coreNodes()).toEqual([
      "10.0.0.1, 10.0.0.2 | MME (4G)",
      "10.0.0.3 | MME (4G)",
      "10.0.1.1 | AMF (5G)",
    ]);
    expect(
      screen.queryByText(
        "The operator whose core sends emergency warnings (PWS) to every cell, through the common CBC.",
      ),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("explains each setting on hover", async () => {
    serve();

    renderAt("/operators/Operator%20A");
    await screen.findByRole("table", { name: "Overview" });

    fireEvent.mouseOver(screen.getByText("PWS Operator"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The operator whose core sends emergency warnings (PWS) to every cell, through the common CBC.",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(screen.getByText(LTE));
    expect(
      await screen.findByRole("tooltip", {
        name: "How Ella MOCN introduces itself to this operator's MMEs, as the eNB it stands for. Assigned by the operator.",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(screen.getByText(NR));
    expect(
      await screen.findByRole("tooltip", {
        name: "How Ella MOCN introduces itself to this operator's AMFs, as the gNB it stands for. Assigned by the operator.",
      }),
    ).toBeInTheDocument();
  });

  it("asks to set the gateway identity before adding MMEs and AMFs", async () => {
    serve();

    renderAt("/operators/Operator%20B");

    await screen.findByRole("heading", { level: 2, name: "Overview" });
    expect(identityOf(LTE)).toBe("Not set");
    expect(identityOf(NR)).toBe("Not set");
    expect(valueOf("Overview", "PWS Operator")).toBe("No");
    expect(screen.getByText("No core network nodes.")).toBeInTheDocument();
    const add = screen.getByRole("button", { name: "Add Core Node" });
    expect(add).toBeDisabled();
    expect(screen.getByRole("button", { name: `Edit ${LTE}` })).toBeEnabled();
    expect(screen.getByRole("button", { name: `Edit ${NR}` })).toBeEnabled();
    const reason = add.parentElement!;
    expect(reason).toHaveAttribute("tabindex", "0");

    fireEvent.keyDown(document.body, { key: "Tab" });
    act(() => (reason as HTMLElement).focus());
    expect(
      await screen.findByRole("tooltip", {
        name: "Set the 4G or 5G gateway identity first. MMEs need the Global eNB ID, AMFs the Global gNB ID.",
      }),
    ).toBeInTheDocument();
  });

  it("asks to remove the MMEs and AMFs before clearing the gateway identity", async () => {
    serve();

    renderAt("/operators/Operator%20A");

    for (const [name, label, kind] of [
      [LTE, "Set a Global eNB ID", "MME"],
      [NR, "Set a Global gNB ID", "AMF"],
    ]) {
      await click(`Edit ${name}`);
      const dialog = within(screen.getByRole("dialog"));
      const set = dialog.getByRole("switch", { name: label });
      expect(set).toBeChecked();
      expect(set).toBeDisabled();
      const reason = set.closest('[tabindex="0"]')!;
      fireEvent.keyDown(document.body, { key: "Tab" });
      fireEvent.focus(reason);
      expect(
        await screen.findByRole("tooltip", {
          name: `Delete this operator's ${kind}s before you remove this identity.`,
        }),
      ).toBeInTheDocument();
      fireEvent.click(dialog.getByRole("button", { name: "Cancel" }));
      await waitFor(() =>
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
      );
    }
  });

  it("shows when the operator does not exist", async () => {
    serve();

    renderAt("/operators/Nobody");

    expect(
      await screen.findByText("Failed to load this operator"),
    ).toBeInTheDocument();
    expect(screen.getByText("Operator not found")).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { level: 2, name: "Overview" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete" }),
    ).not.toBeInTheDocument();
  });

  it("offers no change to a Read Only user", async () => {
    serve();

    renderAt("/operators/Operator%20A", readOnlyAuth);

    await screen.findByRole("heading", { level: 2, name: "Overview" });
    expect(
      screen
        .queryAllByRole("button")
        .map((b) => b.getAttribute("aria-label") ?? b.textContent)
        .filter((name) => /^(Add|Edit|Delete)/.test(name ?? "")),
    ).toEqual([]);
    expect(valueOf("Overview", "PWS Operator")).toBe("Yes");
    expect(coreNodes()).toEqual([
      "10.0.0.1, 10.0.0.2 | MME (4G)",
      "10.0.0.3 | MME (4G)",
      "10.0.1.1 | AMF (5G)",
    ]);
    expect(
      within(screen.getByRole("grid", { name: NODES }))
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(NODE_COLUMNS);
  });

  it("lets a Network Manager change the operator", async () => {
    serve();

    renderAt(
      "/operators/Operator%20A",
      authFor("nm@example.com", RoleID.NetworkManager),
    );

    expect(
      await screen.findByRole("button", { name: "Edit Name" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit" }),
    ).not.toBeInTheDocument();
  });

  it("renames an operator and follows the new name", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Edit Name");

    const dialog = screen.getByRole("dialog", { name: "Rename Operator" });
    expect(within(dialog).getAllByRole("textbox")).toHaveLength(1);
    const rename = within(dialog).getByRole("button", { name: "Rename" });
    expect(rename).toBeDisabled();
    fill("Name", "Operator Z");
    fireEvent.click(rename);

    await saved("Operator renamed successfully.");
    expect(
      await screen.findByRole("heading", {
        level: 1,
        name: /^Operators\s*\/\s*Operator Z$/,
      }),
    ).toBeInTheDocument();
    expect(location()).toBe("/operators/Operator%20Z");
    expect(requests).toEqual([
      put("Operator A", { ...operatorA(), name: "Operator Z" }),
    ]);
  });

  it("edits the PLMNs", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20B");
    await click("Edit PLMNs");

    const dialog = screen.getByRole("dialog", { name: "Edit PLMNs" });
    expect(within(dialog).getByRole("textbox", { name: "MCC" })).toHaveValue(
      "315",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Add PLMN" }));
    fill("MCC", "315", 1);
    fill("MNC", "010", 1);
    blur("MNC", 1);
    expect(within(dialog).getByText("Duplicate PLMN")).toBeInTheDocument();
    fill("MNC", "011", 1);
    for (let i = 2; i < 12; i++) {
      fireEvent.click(within(dialog).getByRole("button", { name: "Add PLMN" }));
    }
    expect(
      within(dialog).getByRole("button", { name: "Add PLMN" }),
    ).toBeDisabled();
    expect(within(dialog).getByText("At most 12 PLMNs")).toBeInTheDocument();
    for (let i = 12; i > 2; i--) {
      fireEvent.click(
        within(dialog).getByRole("button", { name: `Remove PLMN ${i}` }),
      );
    }
    submit("Update");

    await saved("PLMNs updated successfully.");
    expect(requests).toEqual([
      put("Operator B", {
        ...operatorB(),
        plmns: [
          { mcc: "315", mnc: "010" },
          { mcc: "315", mnc: "011" },
        ],
      }),
    ]);
  });

  it("shows the server's error when a removed PLMN is still in use", async () => {
    const requests = serve({
      answer: () =>
        json(400, {
          error: "global_gnb_id plmn must be one of the operator's PLMNs",
        }),
    });

    renderAt("/operators/Operator%20A");
    await click("Edit PLMNs");
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: "Remove PLMN 2",
      }),
    );
    submit("Update");

    expect(
      await screen.findByText(
        "Could not update the PLMNs: global_gnb_id plmn must be one of the operator's PLMNs",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        plmns: [{ mcc: "001", mnc: "01" }],
      }),
    ]);
  });

  it("sets the 4G gateway identity in its own dialog", async () => {
    const requests = serve({
      operators: [
        {
          ...operatorB(),
          plmns: [...operatorB().plmns, { mcc: "001", mnc: "01" }],
        },
      ],
    });

    renderAt("/operators/Operator%20B");
    await click(`Edit ${LTE}`);

    const dialog = screen.getByRole("dialog", {
      name: "Edit 4G Gateway Identity",
    });
    const save = within(dialog).getByRole("button", { name: "Save" });
    for (const textbox of within(dialog).getAllByRole("textbox")) {
      expect(textbox).toBeDisabled();
    }
    const set = within(dialog).getByRole("switch", {
      name: "Set a Global eNB ID",
    });
    expect(set).not.toBeChecked();
    expect(set).toBeEnabled();
    expect(save).toBeDisabled();

    toggle("Set a Global eNB ID");
    expect(save).toBeDisabled();
    choose("PLMN", "001-01");
    fireEvent.mouseDown(
      within(dialog).getByRole("combobox", { name: "eNB ID type" }),
    );
    expect(
      screen.getAllByRole("option").map((option) => option.textContent),
    ).toEqual([
      "Macro (20 bits)",
      "Short macro (18 bits)",
      "Long macro (21 bits)",
    ]);
    fireEvent.click(
      screen.getByRole("option", { name: "Short macro (18 bits)" }),
    );
    expect(within(dialog).getByText("0 to 262143")).toBeInTheDocument();
    fill("eNB ID", "262144");
    expect(save).toBeDisabled();
    expect(
      within(dialog).getByRole("textbox", { name: "eNB ID" }),
    ).not.toHaveAttribute("aria-invalid", "true");
    blur("eNB ID");
    expect(
      within(dialog).getByRole("textbox", { name: "eNB ID" }),
    ).toHaveAttribute("aria-invalid", "true");
    fill("eNB ID", "1234");
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await saved("4G gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator B", {
        ...operatorB(),
        plmns: [
          { mcc: "315", mnc: "010" },
          { mcc: "001", mnc: "01" },
        ],
        global_enb_id: {
          plmn: { mcc: "001", mnc: "01" },
          enb_id_type: "short_macro",
          enb_id: 1234,
        },
      }),
    ]);
    await waitFor(() =>
      expect(identityOf(LTE)).toBe("001-01 · Short macro (18 bits) · 1234"),
    );
    expect(identityOf(NR)).toBe("Not set");
    expect(screen.getByRole("button", { name: "Add Core Node" })).toBeEnabled();
  });

  it("sets the 5G gateway identity in its own dialog", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20B");
    await click(`Edit ${NR}`);

    expect(
      screen.getByRole("dialog", { name: "Edit 5G Gateway Identity" }),
    ).toBeInTheDocument();
    toggle("Set a Global gNB ID");
    expect(submitButton("Save")).toBeDisabled();
    fill("gNB ID", "74565");
    submit("Save");

    await saved("5G gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator B", {
        ...operatorB(),
        global_gnb_id: {
          plmn: { mcc: "315", mnc: "010" },
          gnb_id: 74565,
          bit_length: 22,
        },
      }),
    ]);
    await waitFor(() =>
      expect(identityOf(NR)).toBe("315-010 · 74565 (22 bits)"),
    );
  });

  it("edits the user plane", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Edit User-Plane Aggregation");

    const dialog = within(screen.getByRole("dialog"));
    await waitFor(() =>
      expect(
        dialog.getByRole("switch", { name: "User-plane aggregation" }),
      ).toBeEnabled(),
    );
    expect(dialog.queryAllByRole("textbox")).toEqual([]);
    toggle("User-plane aggregation");
    submit("Save");

    await saved("User plane saved successfully.");
    expect(requests).toEqual([
      put("Operator A", { ...operatorA(), user_plane: { aggregation: false } }),
    ]);
  });

  it("shows the user-plane status the host routing gives", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const userPlane = within(
      await screen.findByRole("table", { name: "User Plane" }),
    );
    const value = async (label: string) =>
      (await userPlane.findByText(label))
        .closest("tr")!
        .querySelectorAll("td")[1].textContent;
    expect(await value("User-Plane Aggregation")).toBe("On");
    expect(await value("Status")).toBe(
      "UnavailableThe route to the operator's MMEs or AMFs changed",
    );
    expect(await value("Interface")).toBe("xfrm-a");
    expect(await value("Addresses")).toBe("10.9.0.1, 2001:db8::1");
  });

  it("keeps user-plane aggregation off when Ella MOCN does not enable it", async () => {
    const requests = serve({ userPlane: false });

    renderAt("/operators/Operator%20A");
    await click("Edit User-Plane Aggregation");

    const dialog = within(screen.getByRole("dialog"));
    const aggregation = dialog.getByRole("switch", {
      name: "User-plane aggregation",
    });
    expect(aggregation).toBeDisabled();
    expect(aggregation).not.toBeChecked();
    fireEvent.mouseOver(aggregation.closest("span") as HTMLElement);
    expect(
      await screen.findByText(
        "User-plane aggregation is not enabled on this Ella MOCN.",
      ),
    ).toBeInTheDocument();
    submit("Save");

    await saved("User plane saved successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        user_plane: { aggregation: false },
      }),
    ]);
  });

  it("edits the Global gNB ID", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click(`Edit ${NR}`);

    const dialog = within(screen.getByRole("dialog"));
    expect(dialog.getByRole("textbox", { name: "gNB ID" })).toHaveValue(
      "74565",
    );
    fill("gNB ID length", "40");
    expect(dialog.queryByText("22 to 32")).not.toBeInTheDocument();
    blur("gNB ID length");
    expect(dialog.getByText("22 to 32")).toBeInTheDocument();
    expect(submitButton("Save")).toBeDisabled();
    fill("gNB ID length", "24");
    fill("gNB ID", "16777216");
    blur("gNB ID");
    expect(dialog.getByText("0 to 16777215")).toBeInTheDocument();
    fill("gNB ID", "99");
    choose("PLMN", "001-01");
    submit("Save");

    await saved("5G gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        global_gnb_id: {
          plmn: { mcc: "001", mnc: "01" },
          gnb_id: 99,
          bit_length: 24,
        },
      }),
    ]);
  });

  it("clears the Global gNB ID once its AMFs are gone", async () => {
    const requests = serve({ operators: [{ ...operatorA(), amfs: [] }] });

    renderAt("/operators/Operator%20A");
    await click(`Edit ${NR}`);
    const dialog = within(screen.getByRole("dialog"));
    expect(
      dialog.getByRole("switch", { name: "Set a Global gNB ID" }),
    ).toBeEnabled();
    toggle("Set a Global gNB ID");
    for (const textbox of dialog.getAllByRole("textbox")) {
      expect(textbox).toBeDisabled();
    }
    submit("Save");

    await saved("5G gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator A", { ...operatorA(), amfs: [], global_gnb_id: undefined }),
    ]);
    await waitFor(() => expect(identityOf(NR)).toBe("Not set"));
    expect(identityOf(LTE)).toBe("001-01 · Macro (20 bits) · 703710");
  });

  it("shows the server's error in the gateway identity dialog", async () => {
    serve({ answer: () => json(500, { error: "database is locked" }) });

    renderAt("/operators/Operator%20A");
    await click(`Edit ${LTE}`);
    fill("eNB ID", "1");
    submit("Save");

    expect(
      await screen.findByText(
        "Could not save the 4G gateway identity: database is locked",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Edit 4G Gateway Identity" }),
    ).toBeInTheDocument();
  });

  it("adds an MME with several addresses", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Add Core Node");

    const dialog = screen.getByRole("dialog", { name: "Add Core Node" });
    expect(
      within(dialog).getByRole("radio", { name: "MME (4G)" }),
    ).toBeChecked();
    expect(
      within(dialog).getByRole("radio", { name: "AMF (5G)" }),
    ).toBeEnabled();
    expect(within(dialog).getByText("Addresses")).toBeInTheDocument();
    const add = within(dialog).getByRole("button", { name: "Add" });
    expect(add).toBeDisabled();
    expect(
      within(dialog).getByRole("button", { name: "Remove address 1" }),
    ).toBeDisabled();
    fill("Address 1", "mme.example.com");
    expect(
      within(dialog).queryByText("Not an IPv4 or IPv6 address"),
    ).not.toBeInTheDocument();
    blur("Address 1");
    expect(
      within(dialog).getByText("Not an IPv4 or IPv6 address"),
    ).toBeInTheDocument();
    fill("Address 1", "10.0.0.3");
    expect(within(dialog).getByText("Already added")).toBeInTheDocument();
    expect(add).toBeDisabled();
    fill("Address 1", "::ffff:10.0.0.3");
    expect(within(dialog).getByText("Already added")).toBeInTheDocument();
    fill("Address 1", "2001:db8::1");
    expect(add).toBeEnabled();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Add address" }),
    );
    expect(add).toBeDisabled();
    fill("Address 2", "2001:DB8:0::1");
    blur("Address 2");
    expect(within(dialog).getByText("Duplicate address")).toBeInTheDocument();
    fill("Address 2", "10.0.0.4");
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Add address" }),
    );
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Remove address 3" }),
    );
    fireEvent.click(add);

    await saved("MME added successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        mmes: [
          ...operatorA().mmes,
          { tnl_addresses: ["2001:db8::1", "10.0.0.4"] },
        ],
      }),
    ]);
  });

  it("edits an MME's addresses", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Edit MME 10.0.0.1, 10.0.0.2");

    const dialog = screen.getByRole("dialog", { name: "Edit MME" });
    const update = within(dialog).getByRole("button", { name: "Update" });
    expect(within(dialog).getByLabelText(/^Address 1/)).toHaveValue("10.0.0.1");
    expect(within(dialog).getByLabelText(/^Address 2/)).toHaveValue("10.0.0.2");
    expect(update).toBeDisabled();
    fill("Address 2", "10.0.0.3");
    blur("Address 2");
    expect(within(dialog).getByText("Already added")).toBeInTheDocument();
    expect(update).toBeDisabled();
    fill("Address 2", "10.0.0.9");
    fireEvent.click(update);

    await saved("MME updated successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        mmes: [
          { tnl_addresses: ["10.0.0.1", "10.0.0.9"] },
          { tnl_addresses: ["10.0.0.3"] },
        ],
      }),
    ]);
  });

  it("adds an AMF", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Add Core Node");
    const dialog = screen.getByRole("dialog", { name: "Add Core Node" });
    fill("Address 1", "10.0.1.1");
    blur("Address 1");
    expect(submitButton("Add")).toBeEnabled();
    fireEvent.click(within(dialog).getByRole("radio", { name: "AMF (5G)" }));
    expect(within(dialog).getByText("Already added")).toBeInTheDocument();
    expect(submitButton("Add")).toBeDisabled();
    fill("Address 1", "10.0.1.2");
    submit("Add");

    await saved("AMF added successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        amfs: [
          { tnl_addresses: ["10.0.1.1"] },
          { tnl_addresses: ["10.0.1.2"] },
        ],
      }),
    ]);
  });

  it("offers only the types whose gateway identity is set", async () => {
    serve({
      operators: [{ ...operatorA(), global_gnb_id: undefined, amfs: [] }],
    });

    renderAt("/operators/Operator%20A");
    await click("Add Core Node");

    const dialog = within(
      screen.getByRole("dialog", { name: "Add Core Node" }),
    );
    expect(dialog.getByRole("radio", { name: "MME (4G)" })).toBeChecked();
    const amf = dialog.getByRole("radio", { name: "AMF (5G)" });
    expect(amf).toBeDisabled();
    fireEvent.mouseOver(dialog.getByText("AMF (5G)"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Set the 5G gateway identity (Global gNB ID) first.",
      }),
    ).toBeInTheDocument();
  });

  it("chooses the AMF when only the Global gNB ID is set", async () => {
    serve({
      operators: [{ ...operatorA(), global_enb_id: undefined, mmes: [] }],
    });

    renderAt("/operators/Operator%20A");
    await click("Add Core Node");

    const dialog = within(
      screen.getByRole("dialog", { name: "Add Core Node" }),
    );
    expect(dialog.getByRole("radio", { name: "MME (4G)" })).toBeDisabled();
    expect(dialog.getByRole("radio", { name: "AMF (5G)" })).toBeChecked();
  });

  it("names the type of the node it edits", async () => {
    serve();

    renderAt("/operators/Operator%20A");
    await click("Edit AMF 10.0.1.1");

    const dialog = within(screen.getByRole("dialog", { name: "Edit AMF" }));
    expect(dialog.queryByRole("radio")).not.toBeInTheDocument();
    expect(dialog.getByLabelText(/^Address 1/)).toHaveValue("10.0.1.1");
  });

  it("deletes an MME after confirming", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Delete MME 10.0.0.1, 10.0.0.2");
    const dialog = screen.getByRole("dialog", { name: "Delete MME" });
    expect(
      within(dialog).getByText(
        "Delete MME 10.0.0.1, 10.0.0.2? Ella MOCN closes its S1 association, and the phones it serves lose their connection.",
      ),
    ).toBeInTheDocument();
    submit("Delete");

    await saved("MME deleted successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        mmes: [{ tnl_addresses: ["10.0.0.3"] }],
      }),
    ]);
  });

  it("shows the server's error when a deletion fails", async () => {
    serve({ answer: () => json(500, { error: "database is locked" }) });

    renderAt("/operators/Operator%20A");
    await click("Delete AMF 10.0.1.1");
    submit("Delete");

    expect(
      await screen.findByText("Failed to delete AMF: database is locked"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Delete AMF" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("edits the PWS operator in a dialog", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20B");
    await click("Edit PWS Operator");

    const dialog = screen.getByRole("dialog", { name: "Edit PWS Operator" });
    const pws = within(dialog).getByRole("switch", { name: "PWS operator" });
    expect(pws).not.toBeChecked();
    const update = within(dialog).getByRole("button", { name: "Update" });
    expect(update).toBeDisabled();
    fireEvent.click(pws);
    expect(update).toBeEnabled();
    fireEvent.click(update);

    await saved("PWS operator updated successfully.");
    expect(requests).toEqual([
      put("Operator B", { ...operatorB(), pws_operator: true }),
    ]);
    await waitFor(() =>
      expect(valueOf("Overview", "PWS Operator")).toBe("Yes"),
    );
  });

  it("reports a PWS conflict in the dialog", async () => {
    serve({
      answer: () =>
        json(409, { error: "Another operator is the PWS operator" }),
    });

    renderAt("/operators/Operator%20B");
    await click("Edit PWS Operator");
    fireEvent.click(
      within(screen.getByRole("dialog")).getByRole("switch", {
        name: "PWS operator",
      }),
    );
    submit("Update");

    expect(
      await screen.findByText(
        "Could not update the PWS operator: Another operator is the PWS operator",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Edit PWS Operator" }),
    ).toBeInTheDocument();
  });

  it("deletes an operator and goes back to the list", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20B");
    await click("Delete");
    const dialog = screen.getByRole("dialog", { name: "Delete Operator" });
    expect(
      within(dialog).getByText(
        "Delete Operator B? Ella MOCN disconnects from its MMEs and AMFs, and its phones lose service on the shared radios. This cannot be undone.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));

    expect(
      await screen.findByText("Operator deleted successfully."),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { level: 1, name: "Operators (1)" }),
    ).toBeInTheDocument();
    expect(location()).toBe("/operators");
    expect(requests).toEqual([
      { method: "DELETE", path: "/api/v1/operators/Operator B" },
    ]);
  });

  it("shows the status of each MME and AMF", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const grid = within(await screen.findByRole("grid", { name: NODES }));
    expect(grid.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(
      NODE_COLUMNS,
    );
    expect(cells(await mmeRow("10.0.0.1, 10.0.0.2"))).toEqual([
      "10.0.0.1, 10.0.0.2",
      "MME (4G)",
      "mme0",
      "2",
      "Up",
      "",
    ]);
    expect(cells(await mmeRow("10.0.0.3"))).toEqual([
      "10.0.0.3",
      "MME (4G)",
      "MME 10.0.0.3",
      "0",
      "Setup failed",
      "",
    ]);
    expect(cells(await mmeRow("10.0.0.4")).slice(2, 5)).toEqual([
      "MME 10.0.0.4",
      "0",
      "Disconnected",
    ]);
    expect(cells(await mmeRow("10.0.0.5")).slice(2, 5)).toEqual([
      "MME 10.0.0.5",
      "—",
      "—",
    ]);
    expect(cells(await nodeRow("AMF", "10.0.1.1")).slice(2, 5)).toEqual([
      "amf0",
      "1",
      "Up",
    ]);
    expect(grid.getAllByText("Up")[0].closest(".MuiChip-root")).toHaveAttribute(
      "title",
      expect.stringMatching(/^Since Nov 12, \d\d:\d\d:00$/),
    );
  });

  it("shows the details of an MME in a dialog", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);
    await click("View MME 10.0.0.1, 10.0.0.2");

    const details = within(screen.getByRole("dialog", { name: "mme0" }));
    const value = (label: string) =>
      details.getByText(label).closest("tr")!.querySelectorAll("td")[0]
        .textContent;
    expect(value("Addresses")).toBe("10.0.0.1, 10.0.0.2");
    expect(value("Type")).toBe("MME (4G)");
    expect(value("Status")).toBe("Up");
    expect(value("MME Name")).toBe("mme0");
    expect(value("Primary Path")).toBe("10.0.0.2");
    expect(value("UE Connections")).toBe("2");
    expect(value("Served GUMMEIs")).toBe(
      "PLMNs 001-01, 001-02 · MME groups 2 · MME codes 1, 3",
    );
    expect(value("Relative MME Capacity")).toBe("255");
    expect(details.queryByText("PLMN Support List")).not.toBeInTheDocument();
    expect(value("Overload")).toBe(
      "GUMMEI 001-01 · MME group 2 · MME code 1: Reject signalling (50%); GUMMEI 001-01 · MME group 2 · MME code 3: Reject signalling (50%)",
    );
    fireEvent.mouseOver(details.getByText("Overload"));
    expect(
      await screen.findByRole("tooltip", {
        name: /^What the node asked to hold back while it is overloaded/,
      }),
    ).toBeInTheDocument();
    fireEvent.click(details.getByRole("button", { name: "Close" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
  });

  it("shows the details of an AMF in a dialog", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);
    await click("View AMF 10.0.1.1");

    const details = within(screen.getByRole("dialog", { name: "amf0" }));
    const value = (label: string) =>
      details.getByText(label).closest("tr")!.querySelectorAll("td")[0]
        .textContent;
    expect(value("AMF Name")).toBe("amf0");
    expect(value("Served GUAMIs")).toBe(
      "001-02 · AMF region 2 · AMF set 1 · AMF pointer 0; 001-02 · AMF region 2 · AMF set 1 · AMF pointer 1 (unavailable)",
    );
    expect(value("PLMN Support List")).toBe("001-02: SST 1, SST 2 SD 010203");
    expect(value("Relative AMF Capacity")).toBe("10");
    expect(value("Overload")).toBe(
      "GUAMI 001-02 · AMF region 2 · AMF set 1 · AMF pointer 0: Emergency and MT only, slice SST 2 SD 010203: 30%; GUAMI 001-02 · AMF region 2 · AMF set 1 · AMF pointer 1: Reject signalling (20%)",
    );
  });

  it("explains the core network nodes on hover", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const grid = within(await screen.findByRole("grid", { name: NODES }));
    fireEvent.mouseOver(grid.getByText("Type"));
    expect(
      await screen.findByRole("tooltip", {
        name: "MME: the 4G core, reached over S1. AMF: the 5G core, reached over NG.",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(grid.getByText("Status"));
    expect(
      await screen.findByRole("tooltip", {
        name: /^Up: connected and ready\./,
      }),
    ).toBeInTheDocument();
  });

  it("keeps the MME status up to date", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const operator = operatorWithStatus();
      serve({ operators: [operator] });

      renderAt("/operators/Operator%20A", readOnlyAuth);
      expect(cells(await mmeRow("10.0.0.4"))[4]).toBe("Disconnected");

      operator.mmes[2].status = {
        state: "connected",
        since: "2026-11-12T10:01:00Z",
        primary_tnl_address: "10.0.0.4",
        ue_connections: 1,
      };
      await vi.advanceTimersByTimeAsync(5000);

      await waitFor(async () =>
        expect(cells(await mmeRow("10.0.0.4")).slice(3, 5)).toEqual([
          "1",
          "Connected",
        ]),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("shows the operator's UE Connections by Radio", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const grid = within(
      await screen.findByRole("grid", { name: "UE Connections by Radio" }),
    );
    expect(grid.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(
      ["Radio", "UE Connections"],
    );
    expect(grid.getAllByRole("row").slice(1).map(cells)).toEqual([
      ["eNB 412", "0"],
      ["enb411", "2"],
    ]);
    expect(grid.getByRole("link", { name: "enb411" })).toHaveAttribute(
      "href",
      "/radios/eNB%3A001-01%3AMacroeNB-0019b",
    );
    expect(grid.getByRole("link", { name: "eNB 412" })).toHaveAttribute(
      "href",
      "/radios/eNB%3A001-01%3AMacroeNB-0019c",
    );

    fireEvent.mouseOver(grid.getByText("Radio"));
    expect(
      await screen.findByRole("tooltip", {
        name: "A radio that broadcasts one of this operator's PLMNs, or that has devices connected to it.",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(grid.getByText("UE Connections"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Devices connected to this operator through the radio: one per active phone, over 4G (S1) or 5G (NG).",
      }),
    ).toBeInTheDocument();
  });

  it("keeps the UE Connections by Radio up to date", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const operator: Operator = {
        ...operatorWithStatus(),
        ue_connections_by_radio: [],
      };
      serve({ operators: [operator] });

      renderAt("/operators/Operator%20A", readOnlyAuth);
      const grid = await screen.findByRole("grid", {
        name: "UE Connections by Radio",
      });
      expect(within(grid).getByText("No radios.")).toBeInTheDocument();

      operator.ue_connections_by_radio = [
        {
          ref: "eNB:001-01:MacroeNB-0019b",
          type: "eNB",
          name: "enb411",
          ue_connections: 1,
        },
      ];
      await vi.advanceTimersByTimeAsync(5000);

      await waitFor(() =>
        expect(within(grid).getAllByRole("row").slice(1).map(cells)).toEqual([
          ["enb411", "1"],
        ]),
      );
    } finally {
      vi.useRealTimers();
    }
  });

  it("sends no MME status back when it changes the operator", async () => {
    const requests = serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A");
    await click("Delete MME 10.0.0.3");
    submit("Delete");

    await saved("MME deleted successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        mmes: [
          { tnl_addresses: ["10.0.0.1", "10.0.0.2"] },
          { tnl_addresses: ["10.0.0.4"] },
          { tnl_addresses: ["10.0.0.5"] },
        ],
        user_plane: { aggregation: true },
      }),
    ]);
  });
});
