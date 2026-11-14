import { expect, vi } from "vitest";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import OperatorDetail from "@/pages/OperatorDetail";
import Operators from "@/pages/Operators";
import type { Operator } from "@/queries/operators";
import { type Auth } from "@/contexts/AuthContext";
import { adminAuth, json, renderWithClient, stubApi } from "@/test/render";

interface Request {
  method: string;
  path: string;
  body?: unknown;
}

export const operatorA = (): Operator => ({
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
    bit_length: 22,
  },
  mmes: [
    { tnl_addresses: ["10.0.0.1", "10.0.0.2"] },
    { tnl_addresses: ["10.0.0.3"] },
  ],
  amfs: [{ tnl_addresses: ["10.0.1.1"] }],
});

export const operatorWithStatus = (): Operator => ({
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
        overload: [
          {
            gummei: {
              plmn: { mcc: "001", mnc: "01" },
              mme_group_id: 2,
              mme_code: 1,
            },
            overload_action: "reject_rrc_cr_signalling",
            traffic_load_reduction_indication: 50,
          },
          {
            gummei: {
              plmn: { mcc: "001", mnc: "01" },
              mme_group_id: 2,
              mme_code: 3,
            },
            overload_action: "reject_rrc_cr_signalling",
            traffic_load_reduction_indication: 50,
          },
        ],
        ue_connections: 2,
      },
    },
    {
      tnl_addresses: ["10.0.0.3"],
      status: {
        state: "setup_failed",
        since: "2026-11-12T10:00:05Z",
        primary_tnl_address: "10.0.0.3",
        ue_connections: 0,
      },
    },
    {
      tnl_addresses: ["10.0.0.4"],
      status: {
        state: "disconnected",
        since: "2026-11-12T10:00:00Z",
        ue_connections: 0,
      },
    },
    { tnl_addresses: ["10.0.0.5"] },
  ],
  amfs: [
    {
      tnl_addresses: ["10.0.1.1"],
      status: {
        state: "up",
        since: "2026-11-12T10:00:00Z",
        primary_tnl_address: "10.0.1.1",
        amf_name: "amf0",
        served_guamis: [
          {
            plmn: { mcc: "001", mnc: "02" },
            amf_region_id: 2,
            amf_set_id: 1,
            amf_pointer: 0,
          },
          {
            plmn: { mcc: "001", mnc: "02" },
            amf_region_id: 2,
            amf_set_id: 1,
            amf_pointer: 1,
          },
        ],
        unavailable_guamis: [
          {
            plmn: { mcc: "001", mnc: "02" },
            amf_region_id: 2,
            amf_set_id: 1,
            amf_pointer: 1,
          },
        ],
        plmn_support: [
          {
            plmn: { mcc: "001", mnc: "02" },
            snssais: [{ sst: 1 }, { sst: 2, sd: "010203" }],
          },
        ],
        relative_amf_capacity: 10,
        overload: [
          {
            guamis: [
              {
                plmn: { mcc: "001", mnc: "02" },
                amf_region_id: 2,
                amf_set_id: 1,
                amf_pointer: 0,
              },
            ],
            overload_action:
              "permit_emergency_sessions_and_mobile_terminated_services_only",
            slices: [
              {
                snssais: [{ sst: 2, sd: "010203" }],
                traffic_load_reduction_indication: 30,
              },
            ],
          },
          {
            guamis: [
              {
                plmn: { mcc: "001", mnc: "02" },
                amf_region_id: 2,
                amf_set_id: 1,
                amf_pointer: 1,
              },
            ],
            overload_action: "reject_rrc_cr_signalling",
            traffic_load_reduction_indication: 20,
          },
        ],
        ue_connections: 1,
      },
    },
  ],
  ue_connections: 3,
  ue_connections_by_radio: [
    {
      ref: "eNB:001-01:MacroeNB-0019c",
      type: "eNB",
      global_enb_id: {
        plmn: { mcc: "001", mnc: "01" },
        enb_id_type: "macro",
        enb_id: 412,
      },
      ue_connections: 0,
    },
    {
      ref: "eNB:001-01:MacroeNB-0019b",
      type: "eNB",
      global_enb_id: {
        plmn: { mcc: "001", mnc: "01" },
        enb_id_type: "macro",
        enb_id: 411,
      },
      name: "enb411",
      ue_connections: 2,
    },
  ],
});

export const operatorB = (): Operator => ({
  name: "Operator B",
  pws_operator: false,
  plmns: [{ mcc: "315", mnc: "010" }],
  mmes: [],
  amfs: [],
});

export const serve = ({
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

export const renderAt = (path: string, auth: Auth = adminAuth) =>
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

export const location = () => screen.getByTestId("location").textContent;

const field = (label: string, index = 0) =>
  within(screen.getByRole("dialog")).getAllByRole("textbox", {
    name: label,
  })[index];

export const fill = (label: string, value: string, index = 0) =>
  fireEvent.change(field(label, index), { target: { value } });

export const blur = (label: string, index = 0) =>
  fireEvent.blur(field(label, index));

const cellText = (cell: Element | null | undefined) => {
  const lines = cell?.querySelectorAll(".MuiStack-root > span") ?? [];
  return lines.length > 0
    ? Array.from(lines, (line) => line.textContent).join(", ")
    : cell?.textContent;
};

export const cells = (row: HTMLElement) =>
  within(row).getAllByRole("gridcell").map(cellText);

export const rowOf = async (text: string) =>
  (await screen.findByText(text)).closest('[role="row"]') as HTMLElement;

export const NODES = /^Core Network Nodes/;

export const nodeRow = async (kind: string, address: string) => {
  const grid = await screen.findByRole("grid", { name: NODES });
  return within(grid)
    .getAllByRole("row")
    .find((row) => {
      const [addresses, type] = Array.from(
        row.querySelectorAll('[role="gridcell"]'),
        cellText,
      );
      return addresses === address && type?.startsWith(`${kind} (`);
    })!;
};

export const mmeRow = (address: string) => nodeRow("MME", address);

export const click = async (name: string) =>
  fireEvent.click(await screen.findByRole("button", { name }));

export const submitButton = (name: string) =>
  within(screen.getByRole("dialog")).getByRole("button", { name });

export const submit = (name: string) => fireEvent.click(submitButton(name));

export const put = (name: string, body: Operator) => ({
  method: "PUT",
  path: `/api/v1/operators/${name}`,
  body,
});

export const saved = async (message: string) => {
  expect(await screen.findByText(message)).toBeInTheDocument();
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
};

export const table = (name: string) =>
  within(screen.getByRole("table", { name }));

export const LTE = "4G Gateway Identity (Global eNB ID)";
export const NR = "5G Gateway Identity (Global gNB ID)";

export const toggle = (label: string) =>
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("switch", { name: label }),
  );

export const choose = (label: string, option: string) => {
  fireEvent.mouseDown(
    within(screen.getByRole("dialog")).getByRole("combobox", { name: label }),
  );
  fireEvent.click(screen.getByRole("option", { name: option }));
};

export const valueOf = (name: string, label: string) =>
  table(name).getByText(label).closest("tr")!.querySelectorAll("td")[1]
    .textContent;

export const identityOf = (label: string) => valueOf("Overview", label);

export const coreNodes = () =>
  within(screen.getByRole("grid", { name: NODES }))
    .queryAllByRole("row")
    .slice(1)
    .map((row) =>
      Array.from(row.querySelectorAll('[role="gridcell"]'), cellText)
        .slice(0, 2)
        .join(" | "),
    );

export const NODE_COLUMNS = [
  "Addresses",
  "Type",
  "Name",
  "UE Connections",
  "Status",
  "Actions",
];
