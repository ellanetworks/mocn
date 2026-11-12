import type {
  ENBIDType,
  GlobalENBID,
  GlobalGNBID,
  Operator,
  PLMN,
} from "@/queries/operators";

export const MAX_NAME_LENGTH = 64;
export const MIN_GNB_ID_LENGTH = 22;
export const MAX_GNB_ID_LENGTH = 32;

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
  "Broadcasts this operator's emergency warnings (PWS) to every cell.";

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

export const formatGlobalENBID = ({ plmn, enb_id_type, enb_id }: GlobalENBID) =>
  `${formatPLMN(plmn)} · ${ENB_ID_TYPE_LABELS[enb_id_type]} (${ENB_ID_BITS[enb_id_type]} bits) · ${enb_id}`;

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

export const addressError = (address: string, existing: string[]) => {
  const trimmed = address.trim();
  if (trimmed === "") return undefined;
  if (!isIPAddress(trimmed)) return "Not an IPv4 or IPv6 address";
  if (existing.includes(trimmed)) return "Already added";
  return undefined;
};

export const operatorBody = (
  operator: Operator,
  change: Partial<Operator> = {},
): Operator => ({
  name: operator.name,
  pws_operator: operator.pws_operator,
  plmns: operator.plmns,
  global_enb_id: operator.global_enb_id ?? null,
  global_gnb_id: operator.global_gnb_id ?? null,
  mmes: operator.mmes ?? [],
  amfs: operator.amfs ?? [],
  ...change,
});
