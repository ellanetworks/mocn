import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import OperatorDetail from "@/pages/OperatorDetail";
import Operators from "@/pages/Operators";
import type { Operator } from "@/queries/operators";
import { RoleID } from "@/queries/users";
import { authFor, type Auth } from "@/contexts/AuthContext";
import {
  adminAuth,
  json,
  readOnlyAuth,
  renderWithClient,
  stubApi,
} from "@/test/render";

interface Request {
  method: string;
  path: string;
  body?: unknown;
}

const operatorA = (): Operator => ({
  name: "Operator A",
  pws_operator: true,
  plmns: [
    { mcc: "001", mnc: "01" },
    { mcc: "001", mnc: "02" },
  ],
  global_enb_id: {
    plmn: { mcc: "001", mnc: "01" },
    enb_id_type: "macro",
    enb_id: 703710,
  },
  global_gnb_id: {
    plmn: { mcc: "001", mnc: "02" },
    gnb_id: 74565,
    gnb_id_length: 22,
  },
  mmes: [
    { tnl_addresses: ["10.0.0.1", "10.0.0.2"] },
    { tnl_addresses: ["10.0.0.3"] },
  ],
  amfs: [{ tnl_addresses: ["10.0.1.1"] }],
});

const operatorWithStatus = (): Operator => ({
  ...operatorA(),
  mmes: [
    {
      tnl_addresses: ["10.0.0.1", "10.0.0.2"],
      status: {
        state: "up",
        since: "2026-11-12T10:00:00Z",
        primary_tnl_address: "10.0.0.2",
        mme_name: "mme0",
        served_gummeis: [
          {
            served_plmns: [
              { mcc: "001", mnc: "01" },
              { mcc: "001", mnc: "02" },
            ],
            served_group_ids: [2],
            served_mmecs: [1, 3],
          },
        ],
        relative_mme_capacity: 255,
      },
    },
    {
      tnl_addresses: ["10.0.0.3"],
      status: {
        state: "setup_failed",
        since: "2026-11-12T10:00:05Z",
        primary_tnl_address: "10.0.0.3",
      },
    },
    {
      tnl_addresses: ["10.0.0.4"],
      status: { state: "disconnected", since: "2026-11-12T10:00:00Z" },
    },
    { tnl_addresses: ["10.0.0.5"] },
  ],
});

const operatorB = (): Operator => ({
  name: "Operator B",
  pws_operator: false,
  plmns: [{ mcc: "315", mnc: "010" }],
  global_enb_id: null,
  global_gnb_id: null,
  mmes: [],
  amfs: [],
});

const serve = ({
  operators = [operatorA(), operatorB()],
  answer,
}: {
  operators?: Operator[];
  answer?: (req: Request) => Response | undefined;
} = {}) => {
  const requests: Request[] = [];
  const handle =
    (fallback: (req: Request) => Response) =>
    (url: URL, init?: RequestInit) => {
      const req: Request = {
        method: init?.method ?? "GET",
        path: decodeURIComponent(url.pathname),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      };
      if (req.method === "GET") return fallback(req);
      requests.push(req);
      return answer?.(req) ?? fallback(req);
    };

  const store = new Map(operators.map((o) => [o.name, o]));
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(String(input), "http://mocn.test");
      const prefix = "/api/v1/operators/";
      if (url.pathname.startsWith(prefix)) {
        const name = decodeURIComponent(url.pathname.slice(prefix.length));
        return handle((req) => {
          const operator = store.get(name);
          if (!operator) return json(404, { error: "Operator not found" });
          if (req.method === "DELETE") {
            store.delete(name);
            return json(200, { result: { message: "Operator deleted" } });
          }
          if (req.method === "PUT") {
            const updated = req.body as Operator;
            store.delete(name);
            store.set(updated.name, updated);
            return json(200, { result: updated });
          }
          return json(200, { result: operator });
        })(url, init);
      }
      return stubApi({
        "/api/v1/operators": handle((req) => {
          if (req.method !== "POST") {
            return json(200, { result: { items: [...store.values()] } });
          }
          const created = req.body as Operator;
          store.set(created.name, created);
          return json(201, { result: created });
        }),
      })(input, init);
    }),
  );
  return requests;
};

function Location() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

const renderAt = (path: string, auth: Auth = adminAuth) =>
  renderWithClient(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/operators" element={<Operators />} />
        <Route path="/operators/:name" element={<OperatorDetail />} />
      </Routes>
      <Location />
    </MemoryRouter>,
    auth,
  );

const location = () => screen.getByTestId("location").textContent;

const field = (label: string, index = 0) =>
  within(screen.getByRole("dialog")).getAllByRole("textbox", {
    name: label,
  })[index];

const fill = (label: string, value: string, index = 0) =>
  fireEvent.change(field(label, index), { target: { value } });

const blur = (label: string, index = 0) => fireEvent.blur(field(label, index));

const cellText = (cell: Element | null | undefined) => {
  const lines = cell?.querySelectorAll(".MuiStack-root > span") ?? [];
  return lines.length > 0
    ? Array.from(lines, (line) => line.textContent).join(", ")
    : cell?.textContent;
};

const cells = (row: HTMLElement) =>
  within(row).getAllByRole("gridcell").map(cellText);

const rowOf = async (text: string) =>
  (await screen.findByText(text)).closest('[role="row"]') as HTMLElement;

const mmeRow = async (address: string) => {
  const grid = await screen.findByRole("grid", { name: /^4G MMEs/ });
  return within(grid)
    .getAllByRole("row")
    .find(
      (row) => cellText(row.querySelector('[role="gridcell"]')) === address,
    )!;
};

const click = async (name: string) =>
  fireEvent.click(await screen.findByRole("button", { name }));

const submitButton = (name: string) =>
  within(screen.getByRole("dialog")).getByRole("button", { name });

const submit = (name: string) => fireEvent.click(submitButton(name));

const put = (name: string, body: Operator) => ({
  method: "PUT",
  path: `/api/v1/operators/${name}`,
  body,
});

const saved = async (message: string) => {
  expect(await screen.findByText(message)).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
};

afterEach(() => {
  vi.unstubAllGlobals();
});

const table = (name: string) => within(screen.getByRole("table", { name }));

const part = (name: string) =>
  within(within(screen.getByRole("dialog")).getByRole("group", { name }));

const LTE = "4G (Global eNB ID)";
const NR = "5G (Global gNB ID)";

const toggle = (name: string) =>
  fireEvent.click(part(name).getByRole("switch", { name: "Set" }));

const partField = (name: string, label: string) =>
  part(name).getByRole("textbox", { name: label });

const fillIn = (name: string, label: string, value: string) =>
  fireEvent.change(partField(name, label), { target: { value } });

const blurIn = (name: string, label: string) =>
  fireEvent.blur(partField(name, label));

const chooseIn = (name: string, label: string, option: string) => {
  fireEvent.mouseDown(part(name).getByRole("combobox", { name: label }));
  fireEvent.click(screen.getByRole("option", { name: option }));
};

const valueOf = (name: string, label: string) =>
  table(name).getByText(label).closest("tr")!.querySelectorAll("td")[1]
    .textContent;

const identityOf = (rat: string) => {
  const cell = table("Overview")
    .getByText("Gateway identity")
    .closest("tr")!
    .querySelectorAll("td")[1];
  const label = Array.from(cell.querySelectorAll("span span")).find(
    (span) => span.textContent === rat,
  )!;
  return label.parentElement!.textContent!.slice(rat.length);
};

const addresses = (name: RegExp) =>
  within(screen.getByRole("grid", { name }))
    .queryAllByRole("row")
    .slice(1)
    .map((row) => cellText(row.querySelector('[role="gridcell"]')));

describe("Operators", () => {
  it("lists the operators, each a link to its page", async () => {
    serve();

    renderAt("/operators");

    expect(
      await screen.findByRole("heading", { level: 1, name: "Operators (2)" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual(["Name", "PLMNs", "PWS"]);
    expect(cells(await rowOf("Operator A"))).toEqual([
      "Operator A",
      "001/01, 001/02",
      "PWS",
    ]);
    expect(cells(await rowOf("Operator B"))).toEqual([
      "Operator B",
      "315/010",
      "",
    ]);
    expect(screen.getByRole("link", { name: "Operator A" })).toHaveAttribute(
      "href",
      "/operators/Operator%20A",
    );
    expect(
      screen.queryByRole("button", { name: /^(Edit|Delete) / }),
    ).not.toBeInTheDocument();
  });

  it("opens an operator when its row is clicked", async () => {
    serve();

    renderAt("/operators");
    fireEvent.click(
      (await rowOf("Operator B")).querySelectorAll('[role="gridcell"]')[1],
    );

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

  it("offers no Create to a Read Only user", async () => {
    serve();

    renderAt("/operators", readOnlyAuth);

    expect(cells(await rowOf("Operator A"))).toHaveLength(3);
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
          global_enb_id: null,
          global_gnb_id: null,
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
      [
        "4G MMEs (2)",
        "The MMEs of this operator's 4G core, each with its TNL addresses.",
      ],
      [
        "5G AMFs (1)",
        "The AMFs of this operator's 5G core, each with its TNL addresses.",
      ],
    ]) {
      expect(
        await screen.findByRole("heading", { level: 2, name }),
      ).toBeInTheDocument();
      expect(screen.getByText(description)).toBeInTheDocument();
    }

    expect(valueOf("Overview", "Name")).toBe("Operator A");
    expect(table("Overview").getByTitle("001/01, 001/02")).toHaveTextContent(
      "001/01, 001/02",
    );
    expect(valueOf("Overview", "PWS operator")).toBe("Yes");
    expect(identityOf("4G")).toBe("001/01 · Macro (20 bits) · 703710");
    expect(identityOf("5G")).toBe("001/02 · 74565 (22 bits)");
    expect(addresses(/^4G MMEs/)).toEqual(["10.0.0.1, 10.0.0.2", "10.0.0.3"]);
    expect(addresses(/^5G AMFs/)).toEqual(["10.0.1.1"]);
    expect(
      screen.queryByText(
        "The operator whose core connects the common CBC. Its emergency warnings (PWS) are broadcast to every cell.",
      ),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("explains each setting on hover", async () => {
    serve();

    renderAt("/operators/Operator%20A");
    await screen.findByRole("table", { name: "Overview" });

    fireEvent.mouseOver(screen.getByText("PWS operator"));
    expect(
      await screen.findByRole("tooltip", {
        name: "The operator whose core connects the common CBC. Its emergency warnings (PWS) are broadcast to every cell.",
      }),
    ).toBeInTheDocument();
    fireEvent.mouseOver(screen.getByText("Gateway identity"));
    expect(
      await screen.findByRole("tooltip", {
        name: "Assigned by the operator. Ella MOCN uses it to identify itself to this operator's core: the Global eNB ID to its MMEs (4G) and the Global gNB ID to its AMFs (5G).",
      }),
    ).toBeInTheDocument();
  });

  it("asks to set the gateway identity before adding MMEs and AMFs", async () => {
    serve();

    renderAt("/operators/Operator%20B");

    await screen.findByRole("heading", { level: 2, name: "Overview" });
    expect(identityOf("4G")).toBe("Not set");
    expect(identityOf("5G")).toBe("Not set");
    expect(valueOf("Overview", "PWS operator")).toBe("No");
    expect(screen.getByText("No MMEs.")).toBeInTheDocument();
    expect(screen.getByText("No AMFs.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add MME" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Add AMF" })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Edit Gateway identity" }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: /^Remove Gateway identity/ }),
    ).not.toBeInTheDocument();
    fireEvent.mouseOver(
      screen.getByRole("button", { name: "Add MME" }).parentElement!,
    );
    expect(
      await screen.findByRole("tooltip", {
        name: "Set the gateway identity first",
      }),
    ).toBeInTheDocument();
  });

  it("asks to remove the MMEs and AMFs before clearing the gateway identity", async () => {
    serve();

    renderAt("/operators/Operator%20A");
    await click("Edit Gateway identity");

    for (const [name, hint] of [
      [LTE, "Delete the MMEs first"],
      [NR, "Delete the AMFs first"],
    ]) {
      const set = part(name).getByRole("switch", { name: "Set" });
      expect(set).toBeChecked();
      expect(set).toBeDisabled();
      expect(set).toHaveAccessibleDescription(hint);
    }
    expect(partField(LTE, "eNB ID")).toHaveValue("703710");
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
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(valueOf("Overview", "PWS operator")).toBe("Yes");
    expect(addresses(/^4G MMEs/)).toEqual(["10.0.0.1, 10.0.0.2", "10.0.0.3"]);
    expect(
      screen.getAllByRole("columnheader").map((h) => h.textContent),
    ).toEqual(["Addresses", "Name", "Served GUMMEIs", "Status", "Addresses"]);
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

  it("sets both IDs of the gateway identity in one dialog", async () => {
    const requests = serve({
      operators: [
        {
          ...operatorB(),
          plmns: [...operatorB().plmns, { mcc: "001", mnc: "01" }],
        },
      ],
    });

    renderAt("/operators/Operator%20B");
    await click("Edit Gateway identity");

    const dialog = screen.getByRole("dialog", {
      name: "Edit Gateway Identity",
    });
    const save = within(dialog).getByRole("button", { name: "Save" });
    for (const textbox of within(dialog).getAllByRole("textbox")) {
      expect(textbox).toBeDisabled();
    }
    for (const name of [LTE, NR]) {
      const set = part(name).getByRole("switch", { name: "Set" });
      expect(set).not.toBeChecked();
      expect(set).toBeEnabled();
    }

    toggle(LTE);
    expect(save).toBeDisabled();
    chooseIn(LTE, "PLMN", "001/01");
    fireEvent.mouseDown(
      part(LTE).getByRole("combobox", { name: "eNB ID type" }),
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
    expect(part(LTE).getByText("0 to 262143")).toBeInTheDocument();
    fillIn(LTE, "eNB ID", "262144");
    expect(save).toBeDisabled();
    expect(partField(LTE, "eNB ID")).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
    blurIn(LTE, "eNB ID");
    expect(partField(LTE, "eNB ID")).toHaveAttribute("aria-invalid", "true");
    fillIn(LTE, "eNB ID", "1234");
    expect(save).toBeEnabled();

    toggle(NR);
    expect(save).toBeDisabled();
    fillIn(NR, "gNB ID", "74565");
    expect(save).toBeEnabled();
    fireEvent.click(save);

    await saved("Gateway identity saved successfully.");
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
        global_gnb_id: {
          plmn: { mcc: "315", mnc: "010" },
          gnb_id: 74565,
          gnb_id_length: 22,
        },
      }),
    ]);
    await waitFor(() =>
      expect(identityOf("4G")).toBe("001/01 · Short macro (18 bits) · 1234"),
    );
    expect(identityOf("5G")).toBe("315/010 · 74565 (22 bits)");
    expect(screen.getByRole("button", { name: "Add MME" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Add AMF" })).toBeEnabled();
  });

  it("edits the Global gNB ID", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Edit Gateway identity");

    expect(partField(NR, "gNB ID")).toHaveValue("74565");
    fillIn(NR, "gNB ID length", "40");
    expect(part(NR).queryByText("22 to 32")).not.toBeInTheDocument();
    blurIn(NR, "gNB ID length");
    expect(part(NR).getByText("22 to 32")).toBeInTheDocument();
    expect(submitButton("Save")).toBeDisabled();
    fillIn(NR, "gNB ID length", "24");
    fillIn(NR, "gNB ID", "16777216");
    blurIn(NR, "gNB ID");
    expect(part(NR).getByText("0 to 16777215")).toBeInTheDocument();
    fillIn(NR, "gNB ID", "99");
    chooseIn(NR, "PLMN", "001/01");
    submit("Save");

    await saved("Gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator A", {
        ...operatorA(),
        global_gnb_id: {
          plmn: { mcc: "001", mnc: "01" },
          gnb_id: 99,
          gnb_id_length: 24,
        },
      }),
    ]);
  });

  it("clears the Global gNB ID once its AMFs are gone", async () => {
    const requests = serve({ operators: [{ ...operatorA(), amfs: [] }] });

    renderAt("/operators/Operator%20A");
    await click("Edit Gateway identity");
    expect(part(LTE).getByRole("switch", { name: "Set" })).toBeDisabled();
    expect(
      part(NR).queryByText("Delete the AMFs first"),
    ).not.toBeInTheDocument();
    toggle(NR);
    for (const textbox of part(NR).getAllByRole("textbox")) {
      expect(textbox).toBeDisabled();
    }
    submit("Save");

    await saved("Gateway identity saved successfully.");
    expect(requests).toEqual([
      put("Operator A", { ...operatorA(), amfs: [], global_gnb_id: null }),
    ]);
    await waitFor(() => expect(identityOf("5G")).toBe("Not set"));
    expect(identityOf("4G")).toBe("001/01 · Macro (20 bits) · 703710");
  });

  it("shows the server's error in the gateway identity dialog", async () => {
    serve({ answer: () => json(500, { error: "database is locked" }) });

    renderAt("/operators/Operator%20A");
    await click("Edit Gateway identity");
    fillIn(LTE, "eNB ID", "1");
    submit("Save");

    expect(
      await screen.findByText(
        "Could not save the gateway identity: database is locked",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Edit Gateway Identity" }),
    ).toBeInTheDocument();
  });

  it("adds an MME with several addresses", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Add MME");

    const dialog = screen.getByRole("dialog", { name: "Add MME" });
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
    fill("Address 1", "2001:db8::1");
    expect(add).toBeEnabled();

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Add address" }),
    );
    expect(add).toBeDisabled();
    fill("Address 2", "2001:db8::1");
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
          ...operatorA().mmes!,
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
    await click("Add AMF");
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

  it("deletes an MME after confirming", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20A");
    await click("Delete MME 10.0.0.1, 10.0.0.2");
    const dialog = screen.getByRole("dialog", { name: "Delete MME" });
    expect(
      within(dialog).getByText("Delete MME 10.0.0.1, 10.0.0.2?"),
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
      await screen.findByText("Could not delete: database is locked"),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("dialog", { name: "Delete AMF" }),
    ).toBeInTheDocument();
  });

  it("edits the PWS operator in a dialog", async () => {
    const requests = serve();

    renderAt("/operators/Operator%20B");
    await click("Edit PWS operator");

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
      expect(valueOf("Overview", "PWS operator")).toBe("Yes"),
    );
  });

  it("reports a PWS conflict in the dialog", async () => {
    serve({
      answer: () =>
        json(409, { error: "Another operator is the PWS operator" }),
    });

    renderAt("/operators/Operator%20B");
    await click("Edit PWS operator");
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
      within(dialog).getByText("Delete Operator B? This cannot be undone."),
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

  it("shows the S1 status of each MME", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const grid = within(await screen.findByRole("grid", { name: /^4G MMEs/ }));
    expect(grid.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(
      ["Addresses", "Name", "Served GUMMEIs", "Status"],
    );
    expect(cells(await mmeRow("10.0.0.1, 10.0.0.2"))).toEqual([
      "10.0.0.1, 10.0.0.2",
      "mme0",
      "PLMNs 001/01, 001/02 · MME groups 2 · MME codes 1, 3",
      "Up",
    ]);
    expect(cells(await mmeRow("10.0.0.3"))).toEqual([
      "10.0.0.3",
      "—",
      "—",
      "Setup failed",
    ]);
    expect(cells(await mmeRow("10.0.0.4"))).toEqual([
      "10.0.0.4",
      "—",
      "—",
      "Disconnected",
    ]);
    expect(cells(await mmeRow("10.0.0.5"))).toEqual([
      "10.0.0.5",
      "—",
      "—",
      "—",
    ]);
    expect(
      within(screen.getByRole("grid", { name: /^5G AMFs/ }))
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(["Addresses"]);
  });

  it("explains the MME status on hover", async () => {
    serve({ operators: [operatorWithStatus()] });

    renderAt("/operators/Operator%20A", readOnlyAuth);

    const grid = within(await screen.findByRole("grid", { name: /^4G MMEs/ }));
    fireEvent.mouseOver(grid.getByText("Status"));
    expect(
      await screen.findByRole("tooltip", {
        name: /^Disconnected: no SCTP association to the MME\./,
      }),
    ).toBeInTheDocument();
    expect(grid.getByText("Up").closest(".MuiChip-root")).toHaveAttribute(
      "title",
      expect.stringMatching(/^Since Nov 12, \d\d:\d\d:00$/),
    );
  });

  it("keeps the MME status up to date", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const operator = operatorWithStatus();
      serve({ operators: [operator] });

      renderAt("/operators/Operator%20A", readOnlyAuth);
      expect(cells(await mmeRow("10.0.0.4"))[3]).toBe("Disconnected");

      operator.mmes![2].status = {
        state: "connected",
        since: "2026-11-12T10:01:00Z",
        primary_tnl_address: "10.0.0.4",
      };
      await vi.advanceTimersByTimeAsync(5000);

      await waitFor(async () =>
        expect(cells(await mmeRow("10.0.0.4"))[3]).toBe("Connected"),
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
      }),
    ]);
  });
});
