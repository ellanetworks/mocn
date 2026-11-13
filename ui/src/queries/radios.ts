import type { AnyENBIDType, GlobalGNBID, PLMN } from "@/queries/operators";
import { apiFetch } from "@/queries/utils";

export type RadioState = "connected" | "up";

export type RadioInterface = "s1" | "ng";

export interface RadioGlobalENBID {
  plmn: PLMN;
  enb_id_type: AnyENBIDType;
  enb_id: number;
}

export interface SupportedTA {
  tac: number;
  broadcast_plmns: PLMN[];
}

export interface Radio {
  ref?: string;
  interface: RadioInterface;
  global_enb_id?: RadioGlobalENBID | null;
  global_gnb_id?: GlobalGNBID | null;
  enb_name?: string;
  ran_node_name?: string;
  supported_tas: SupportedTA[];
  default_paging_drx?: number;
  state: RadioState;
  connected_since: string;
  tnl_addresses: string[];
  ue_connections: number;
  ue_connections_by_operator?: OperatorUEConnections[];
}

export interface OperatorUEConnections {
  operator: string;
  ue_connections: number;
}

export const listRadios = async (): Promise<Radio[]> => {
  const { items } = await apiFetch<{ items: Radio[] }>("/api/v1/radios");
  return items;
};

export const radioPath = (ref: string) => `/radios/${encodeURIComponent(ref)}`;

export const getRadio = (ref: string): Promise<Radio> =>
  apiFetch<Radio>(`/api/v1/radios/${encodeURIComponent(ref)}`);

export const radioName = (radio: Radio) =>
  radio.enb_name ?? radio.ran_node_name;

export const radioLabel = (radio: Radio) =>
  radioName(radio) ?? radio.ref ?? radio.tnl_addresses.join(", ");
