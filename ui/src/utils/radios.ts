import type { Radio, RadioInterface } from "@/queries/radios";
import { formatGlobalENBID, formatGlobalGNBID } from "@/utils/operators";

export const RADIO_TYPES: Record<RadioInterface, string> = {
  s1: "eNB (4G)",
  ng: "gNB (5G)",
};

export const formatRadioID = (radio: Radio) => {
  if (radio.global_enb_id) return formatGlobalENBID(radio.global_enb_id);
  if (radio.global_gnb_id) return formatGlobalGNBID(radio.global_gnb_id);
  return undefined;
};
