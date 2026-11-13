import type { AnyENBIDType, PLMN } from "@/queries/operators";
import { apiFetch } from "@/queries/utils";

export type RadioState = "connected" | "up";

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
  global_enb_id: RadioGlobalENBID | null;
  enb_name?: string;
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

export const radioLabel = (radio: Radio) =>
  radio.enb_name ?? radio.ref ?? radio.tnl_addresses.join(", ");
