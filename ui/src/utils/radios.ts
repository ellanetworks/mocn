import type { RANNodeType } from "@/queries/operators";
import type { Radio, RadioSetupFailure } from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { formatGlobalENBID, formatGlobalGNBID } from "@/utils/operators";

export const RADIO_TYPES: Record<RANNodeType, string> = {
  eNB: "eNB (4G)",
  gNB: "gNB (5G)",
};

export const RADIO_TYPE_HELP =
  "eNB: a 4G radio, connected over S1. gNB: a 5G radio, connected over NG.";

export const formatRadioID = (radio: Radio) => {
  if (radio.global_enb_id) return formatGlobalENBID(radio.global_enb_id);
  if (radio.global_gnb_id) return formatGlobalGNBID(radio.global_gnb_id);
  return undefined;
};

export const RADIO_STATE_HELP =
  "Up: connected and ready. Connected: link up, setup not done yet (S1 Setup or NG Setup). Setup failed: link up, Ella MOCN refused the radio's last setup.";

export const radioRowID = (radio: Radio) =>
  radio.ref ??
  `${radio.type} ${radio.tnl_addresses.join(",")} ${radio.connected_at}`;

export const sameRadio = (a: Radio, b: Radio) =>
  a.ref || b.ref
    ? a.ref === b.ref
    : a.type === b.type &&
      a.tnl_addresses.join(",") === b.tnl_addresses.join(",");

export const formatSetupFailure = ({ cause, reason }: RadioSetupFailure) =>
  `${cause}: ${reason}`;

export const radioStateHint = (radio: Radio) =>
  radio.setup_failure
    ? `${formatSetupFailure(radio.setup_failure)}. Refused at ${formatDateTime(radio.setup_failure.at, { seconds: true })}`
    : `Connected at ${formatDateTime(radio.connected_at, { seconds: true })}`;
