import type {
  AMFOverload,
  AnyENBIDType,
  CoreNode,
  ENBIDType,
  GlobalENBID,
  GlobalGNBID,
  GUAMI,
  Operator,
  Overload,
  OverloadAction,
  PLMN,
  PLMNSupport,
  ServedGUMMEI,
  SNSSAI,
} from "@/queries/operators";

export const MAX_NAME_LENGTH = 64;
export const MIN_GNB_ID_LENGTH = 22;
export const MAX_GNB_ID_LENGTH = 32;
export const MAX_PLMNS = 12;

export const ENB_ID_BITS: Record<ENBIDType, number> = {
  macro: 20,
  short_macro: 18,
  long_macro: 21,
};

export const ENB_ID_TYPE_LABELS: Record<ENBIDType, string> = {
  macro: "Macro",
  short_macro: "Short macro",
  long_macro: "Long macro",
};

const ANY_ENB_ID_BITS: Record<AnyENBIDType, number> = {
  ...ENB_ID_BITS,
  home: 28,
};

const ANY_ENB_ID_TYPE_LABELS: Record<AnyENBIDType, string> = {
  ...ENB_ID_TYPE_LABELS,
  home: "Home",
};

export const PWS_HELP =
  "The operator whose core connects the common CBC. Its emergency warnings (PWS) are broadcast to every cell.";

export const GATEWAY_IDENTITY_HELP =
  "Assigned by the operator. Ella MOCN uses it to identify itself to this operator's core: the Global eNB ID to its MMEs (4G) and the Global gNB ID to its AMFs (5G).";

export const ENB_IDENTITY_LABEL = "4G (Global eNB ID)";

export const GNB_IDENTITY_LABEL = "5G (Global gNB ID)";

export const ENB_ID_TYPES = Object.keys(ENB_ID_BITS) as ENBIDType[];

export interface PLMNRow {
  key: number;
  mcc: string;
  mnc: string;
}

export interface PLMNErrors {
  mcc?: string;
  mnc?: string;
}

let nextKey = 0;

export const plmnRow = (mcc = "", mnc = ""): PLMNRow => ({
  key: nextKey++,
  mcc,
  mnc,
});

export const operatorPath = (name: string) =>
  `/operators/${encodeURIComponent(name)}`;

export const formatPLMN = ({ mcc, mnc }: PLMN) => `${mcc}/${mnc}`;

export const parsePLMN = (text: string): PLMN => {
  const [mcc = "", mnc = ""] = text.split("/");
  return { mcc, mnc };
};

export const formatGlobalENBID = ({
  plmn,
  enb_id_type,
  enb_id,
}: Omit<GlobalENBID, "enb_id_type"> & { enb_id_type: AnyENBIDType }) =>
  `${formatPLMN(plmn)} · ${ANY_ENB_ID_TYPE_LABELS[enb_id_type]} (${ANY_ENB_ID_BITS[enb_id_type]} bits) · ${enb_id}`;

export const formatGUMMEI = ({
  served_plmns,
  served_group_ids,
  served_mmecs,
}: ServedGUMMEI) =>
  [
    `PLMNs ${served_plmns.map(formatPLMN).join(", ")}`,
    `MME groups ${served_group_ids.join(", ")}`,
    `MME codes ${served_mmecs.join(", ")}`,
  ].join(" · ");

export const OVERLOAD_ACTION_LABELS: Record<OverloadAction, string> = {
  reject_non_emergency_mo_dt: "Reject non-emergency MO data",
  reject_rrc_cr_signalling: "Reject signalling",
  permit_emergency_sessions_and_mobile_terminated_services_only:
    "Emergency and MT only",
  permit_high_priority_sessions_and_mobile_terminated_services_only:
    "High priority and MT only",
  reject_delay_tolerant_access: "Reject delay tolerant",
  permit_high_priority_sessions_and_exception_reporting_and_mobile_terminated_services_only:
    "High priority, exception reporting and MT only",
  not_accept_mo_data_or_delay_tolerant_access_from_cp_ciot:
    "Reject CP CIoT MO data",
};

const formatOverloadAction = (o: Overload) =>
  (OVERLOAD_ACTION_LABELS[o.overload_action] ?? o.overload_action) +
  (o.traffic_load_reduction_indication === undefined
    ? ""
    : ` (${o.traffic_load_reduction_indication}%)`);

export const formatOverload = (overload: Overload[]) =>
  [...new Set(overload.map(formatOverloadAction))].join("; ");

export const formatOverloadDetail = (overload: Overload[]) =>
  overload
    .map(
      (o) =>
        `GUMMEI ${formatPLMN(o.gummei.plmn)} · MME group ${o.gummei.mme_group_id} · MME code ${o.gummei.mme_code}: ${formatOverloadAction(o)}`,
    )
    .join("; ");

export const formatGUAMI = ({
  plmn,
  amf_region_id,
  amf_set_id,
  amf_pointer,
}: GUAMI) =>
  `${formatPLMN(plmn)} · AMF region ${amf_region_id} · AMF set ${amf_set_id} · AMF pointer ${amf_pointer}`;

const sameGUAMI = (a: GUAMI, b: GUAMI) => formatGUAMI(a) === formatGUAMI(b);

export const formatServedGUAMIs = (
  served: GUAMI[],
  unavailable: GUAMI[] = [],
) =>
  served
    .map(
      (g) =>
        formatGUAMI(g) +
        (unavailable.some((u) => sameGUAMI(u, g)) ? " (unavailable)" : ""),
    )
    .join("; ");

export const formatSNSSAI = ({ sst, sd }: SNSSAI) =>
  sd === undefined ? `SST ${sst}` : `SST ${sst} SD ${sd}`;

export const formatPLMNSupport = (support: PLMNSupport[]) =>
  support
    .map(
      ({ plmn, snssais }) =>
        `${formatPLMN(plmn)}: ${snssais.map(formatSNSSAI).join(", ")}`,
    )
    .join("; ");

const formatActionAndReduction = (
  action?: OverloadAction,
  reduction?: number,
) => {
  const label =
    action === undefined
      ? undefined
      : (OVERLOAD_ACTION_LABELS[action] ?? action);
  if (label === undefined) {
    return reduction === undefined ? "" : `${reduction}%`;
  }
  return reduction === undefined ? label : `${label} (${reduction}%)`;
};

export const formatAMFOverload = (o: AMFOverload) =>
  formatActionAndReduction(
    o.overload_action,
    o.traffic_load_reduction_indication,
  ) || "Slices";

export const formatAMFOverloadDetail = (o: AMFOverload) =>
  [
    `AMF: ${formatActionAndReduction(o.overload_action, o.traffic_load_reduction_indication) || "no action"}`,
    ...(o.slices ?? []).map(
      (s) =>
        `${s.snssais.map(formatSNSSAI).join(", ")}: ${formatActionAndReduction(s.overload_action, s.traffic_load_reduction_indication) || "no action"}`,
    ),
  ].join("; ");

export const formatGlobalGNBID = ({
  plmn,
  gnb_id,
  gnb_id_length,
}: GlobalGNBID) => `${formatPLMN(plmn)} · ${gnb_id} (${gnb_id_length} bits)`;

export const isMCC = (mcc: string) => /^\d{3}$/.test(mcc);

export const isMNC = (mnc: string) => /^\d{2,3}$/.test(mnc);

const IPV4 =
  /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;

export const isIPAddress = (address: string): boolean => {
  if (IPV4.test(address)) return true;
  return (
    address.includes(":") &&
    /^[0-9a-fA-F:.]+$/.test(address) &&
    URL.canParse(`http://[${address}]/`)
  );
};

const inRange = (text: string, min: number, max: number) =>
  /^\d+$/.test(text) && Number(text) >= min && Number(text) <= max;

export const maxENBID = (type: ENBIDType) => 2 ** ENB_ID_BITS[type] - 1;

export const maxGNBID = (length: string) =>
  inRange(length, MIN_GNB_ID_LENGTH, MAX_GNB_ID_LENGTH)
    ? 2 ** Number(length) - 1
    : undefined;

export const nameError = (name: string) =>
  name.trim().length > MAX_NAME_LENGTH
    ? `At most ${MAX_NAME_LENGTH} characters`
    : undefined;

export const requiredError = (value: string, error?: string) =>
  value.trim() === "" ? "Required" : error;

export const isValidName = (name: string) =>
  name.trim() !== "" && nameError(name) === undefined;

export const plmnErrors = (rows: PLMNRow[]): PLMNErrors[] => {
  const seen = new Set<string>();
  return rows.map(({ mcc, mnc }) => {
    const errors: PLMNErrors = {};
    if (mcc !== "" && !isMCC(mcc)) errors.mcc = "3 digits";
    if (mnc !== "" && !isMNC(mnc)) errors.mnc = "2 or 3 digits";
    if (isMCC(mcc) && isMNC(mnc)) {
      const key = formatPLMN({ mcc, mnc });
      if (seen.has(key)) errors.mnc = "Duplicate PLMN";
      seen.add(key);
    }
    return errors;
  });
};

export const isValidPLMNs = (rows: PLMNRow[]) =>
  rows.length > 0 &&
  rows.length <= MAX_PLMNS &&
  rows.every((p) => p.mcc !== "" && p.mnc !== "") &&
  plmnErrors(rows).every((e) => e.mcc === undefined && e.mnc === undefined);

export const plmnRows = (plmns: PLMN[]) =>
  plmns.map((p) => plmnRow(p.mcc, p.mnc));

export const plmnParams = (rows: PLMNRow[]): PLMN[] =>
  rows.map(({ mcc, mnc }) => ({ mcc, mnc }));

export const enbIdError = (type: ENBIDType, id: string) =>
  id !== "" && !inRange(id, 0, maxENBID(type))
    ? `0 to ${maxENBID(type)}`
    : undefined;

export const gnbIdLengthError = (length: string) =>
  maxGNBID(length) === undefined
    ? `${MIN_GNB_ID_LENGTH} to ${MAX_GNB_ID_LENGTH}`
    : undefined;

export const gnbIdError = (id: string, length: string) => {
  const max = maxGNBID(length);
  return max !== undefined && id !== "" && !inRange(id, 0, max)
    ? `0 to ${max}`
    : undefined;
};

export interface AddressRow {
  key: number;
  address: string;
}

export const addressRow = (address = ""): AddressRow => ({
  key: nextKey++,
  address,
});

export const formatTNLAddresses = ({ tnl_addresses }: CoreNode) =>
  tnl_addresses.join(", ");

export const addressErrors = (rows: AddressRow[], existing: CoreNode[]) => {
  const taken = new Set(existing.flatMap((n) => n.tnl_addresses));
  const seen = new Set<string>();
  return rows.map(({ address }) => {
    const trimmed = address.trim();
    if (trimmed === "") return undefined;
    if (!isIPAddress(trimmed)) return "Not an IPv4 or IPv6 address";
    if (taken.has(trimmed)) return "Already added";
    if (seen.has(trimmed)) return "Duplicate address";
    seen.add(trimmed);
    return undefined;
  });
};

export const isValidAddresses = (rows: AddressRow[], existing: CoreNode[]) =>
  rows.length > 0 &&
  rows.every((r) => r.address.trim() !== "") &&
  addressErrors(rows, existing).every((e) => e === undefined);

const nodeBody = ({ tnl_addresses }: CoreNode): CoreNode => ({
  tnl_addresses,
});

export const operatorBody = (
  operator: Operator,
  change: Partial<Operator> = {},
): Operator => {
  const merged = { ...operator, ...change };
  return {
    name: merged.name,
    pws_operator: merged.pws_operator,
    plmns: merged.plmns,
    global_enb_id: merged.global_enb_id ?? null,
    global_gnb_id: merged.global_gnb_id ?? null,
    mmes: (merged.mmes ?? []).map(nodeBody),
    amfs: (merged.amfs ?? []).map(nodeBody),
  };
};
