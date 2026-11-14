import type {
  AMFOverload,
  CoreNode,
  CoreNodeKind,
  ENBIDType,
  GlobalENBID,
  GlobalGNBID,
  GUAMI,
  GUMMEI,
  MMEOverload,
  MMEOverloadAction,
  Operator,
  PLMN,
  PLMNSupport,
  ServedGUMMEIsItem,
  SNSSAI,
} from "@/queries/operators";

export const MAX_NAME_LENGTH = 64;
export const MIN_GNB_ID_BIT_LENGTH = 22;
export const MAX_GNB_ID_BIT_LENGTH = 32;
export const MAX_PLMNS = 12;

export const ENB_ID_BITS: Record<ENBIDType, number> = {
  macro: 20,
  home: 28,
  short_macro: 18,
  long_macro: 21,
};

export const ENB_ID_TYPE_LABELS: Record<ENBIDType, string> = {
  macro: "Macro",
  home: "Home",
  short_macro: "Short macro",
  long_macro: "Long macro",
};

export const PWS_HELP =
  "The operator whose core sends emergency warnings (PWS) to every cell, through the common CBC.";

export const ENB_IDENTITY_LABEL = "4G Gateway Identity (Global eNB ID)";

export const GNB_IDENTITY_LABEL = "5G Gateway Identity (Global gNB ID)";

export const ENB_IDENTITY_HELP =
  "How Ella MOCN introduces itself to this operator's MMEs, as the eNB it stands for. Assigned by the operator.";

export const GNB_IDENTITY_HELP =
  "How Ella MOCN introduces itself to this operator's AMFs, as the gNB it stands for. Assigned by the operator.";

export const OPERATOR_ENB_ID_TYPES: ENBIDType[] = [
  "macro",
  "short_macro",
  "long_macro",
];

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

export const CORE_NODE_KIND_LABELS: Record<CoreNodeKind, string> = {
  MME: "MME (4G)",
  AMF: "AMF (5G)",
};

export const coreNodeKey = ({ tnl_addresses }: CoreNode) =>
  tnl_addresses.map(canonicalAddress).sort().join(",");

export const operatorPath = (name: string) =>
  `/operators/${encodeURIComponent(name)}`;

export const formatPLMN = ({ mcc, mnc }: PLMN) => `${mcc}-${mnc}`;

export const parsePLMN = (text: string): PLMN => {
  const [mcc = "", mnc = ""] = text.split("-");
  return { mcc, mnc };
};

export const formatGlobalENBID = ({ plmn, enb_id_type, enb_id }: GlobalENBID) =>
  `${formatPLMN(plmn)} · ${ENB_ID_TYPE_LABELS[enb_id_type]} (${ENB_ID_BITS[enb_id_type]} bits) · ${enb_id}`;

export const formatServedGUMMEIsItem = ({
  served_plmns,
  served_group_ids,
  served_mmecs,
}: ServedGUMMEIsItem) =>
  [
    `PLMNs ${served_plmns.map(formatPLMN).join(", ")}`,
    `MME groups ${served_group_ids.join(", ")}`,
    `MME codes ${served_mmecs.join(", ")}`,
  ].join(" · ");

export const formatGUMMEI = ({ plmn, mme_group_id, mme_code }: GUMMEI) =>
  `${formatPLMN(plmn)} · MME group ${mme_group_id} · MME code ${mme_code}`;

export const OVERLOAD_ACTION_LABELS: Record<MMEOverloadAction, string> = {
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

const formatOverloadAction = (o: MMEOverload) =>
  (OVERLOAD_ACTION_LABELS[o.overload_action] ?? o.overload_action) +
  (o.traffic_load_reduction_indication === undefined
    ? ""
    : ` (${o.traffic_load_reduction_indication}%)`);

export const formatMMEOverload = (overload: MMEOverload[]) =>
  [...new Set(overload.map(formatOverloadAction))].join("; ");

export const formatMMEOverloadDetail = (overload: MMEOverload[]) =>
  overload
    .map((o) => `GUMMEI ${formatGUMMEI(o.gummei)}: ${formatOverloadAction(o)}`)
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
  action?: MMEOverloadAction,
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

const formatActionOrNone = (action?: MMEOverloadAction, reduction?: number) =>
  formatActionAndReduction(action, reduction) || "no action";

export const formatAMFOverloadDetail = (overload: AMFOverload[]) =>
  overload
    .map(
      (o) =>
        `${o.guamis.map((g) => `GUAMI ${formatGUAMI(g)}`).join(", ")}: ` +
        [
          formatActionOrNone(
            o.overload_action,
            o.traffic_load_reduction_indication,
          ),
          ...(o.slices ?? []).map(
            (s) =>
              `slice ${s.snssais.map(formatSNSSAI).join(", ")}: ${formatActionOrNone(s.overload_action, s.traffic_load_reduction_indication)}`,
          ),
        ].join(", "),
    )
    .join("; ");

export const formatGlobalGNBID = ({ plmn, gnb_id, bit_length }: GlobalGNBID) =>
  `${formatPLMN(plmn)} · ${gnb_id} (${bit_length} bits)`;

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

const MAPPED_IPV4 = /^::ffff:([0-9a-f]{1,4}):([0-9a-f]{1,4})$/;

export const canonicalAddress = (address: string): string => {
  if (!isIPAddress(address)) return address;
  if (IPV4.test(address)) return address;
  const host = new URL(`http://[${address}]/`).hostname.slice(1, -1);
  const mapped = MAPPED_IPV4.exec(host);
  if (!mapped) return host;
  const [high, low] = [parseInt(mapped[1], 16), parseInt(mapped[2], 16)];
  return [high >> 8, high & 255, low >> 8, low & 255].join(".");
};

const inRange = (text: string, min: number, max: number) =>
  /^\d+$/.test(text) && Number(text) >= min && Number(text) <= max;

export const maxENBID = (type: ENBIDType) => 2 ** ENB_ID_BITS[type] - 1;

export const maxGNBID = (bitLength: string) =>
  inRange(bitLength, MIN_GNB_ID_BIT_LENGTH, MAX_GNB_ID_BIT_LENGTH)
    ? 2 ** Number(bitLength) - 1
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

export const gnbIdBitLengthError = (bitLength: string) =>
  maxGNBID(bitLength) === undefined
    ? `${MIN_GNB_ID_BIT_LENGTH} to ${MAX_GNB_ID_BIT_LENGTH}`
    : undefined;

export const gnbIdError = (id: string, bitLength: string) => {
  const max = maxGNBID(bitLength);
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

export const formatAddresses = ({ tnl_addresses }: CoreNode) =>
  tnl_addresses.join(", ");

export const addressErrors = (rows: AddressRow[], existing: CoreNode[]) => {
  const taken = new Set(
    existing.flatMap((n) => n.tnl_addresses).map(canonicalAddress),
  );
  const seen = new Set<string>();
  return rows.map(({ address }) => {
    const trimmed = address.trim();
    if (trimmed === "") return undefined;
    if (!isIPAddress(trimmed)) return "Not an IPv4 or IPv6 address";
    const canonical = canonicalAddress(trimmed);
    if (taken.has(canonical)) return "Already added";
    if (seen.has(canonical)) return "Duplicate address";
    seen.add(canonical);
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
    global_enb_id: merged.global_enb_id,
    global_gnb_id: merged.global_gnb_id,
    mmes: merged.mmes.map(nodeBody),
    amfs: merged.amfs.map(nodeBody),
  };
};

export const coreNodesUp = ({ mmes, amfs }: Operator) => {
  const nodes = [...mmes, ...amfs];
  return {
    up: nodes.filter((n) => n.status?.state === "up").length,
    total: nodes.length,
  };
};
