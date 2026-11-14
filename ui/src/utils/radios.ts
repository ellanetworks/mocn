import type { RANNodeType } from "@/queries/operators";
import type { Radio } from "@/queries/radios";
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
