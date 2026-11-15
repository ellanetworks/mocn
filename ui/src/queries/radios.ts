import type {
  GlobalENBID,
  GlobalGNBID,
  PLMN,
  RANNodeType,
  SNSSAI,
} from "@/queries/operators";
import {
  apiFetch,
  type Page,
  type PageParams,
  withQuery,
} from "@/queries/utils";

export type RadioState = "connected" | "up" | "setup_failed";

export interface RadioSetupFailure {
  cause: string;
  reason: string;
  at: string;
}

export interface BroadcastPLMN {
  plmn: PLMN;
  snssais?: SNSSAI[];
}

export interface SupportedTA {
  tac: string;
  broadcast_plmns: BroadcastPLMN[];
}

export interface Radio {
  ref?: string;
  type: RANNodeType;
  global_enb_id?: GlobalENBID;
  global_gnb_id?: GlobalGNBID;
  name?: string;
  supported_tas: SupportedTA[];
  default_paging_drx?: number;
  state: RadioState;
  setup_failure?: RadioSetupFailure;
  connected_at: string;
  tnl_addresses: string[];
  ue_connections: number;
  ue_connections_by_operator?: OperatorUEConnections[];
}

export interface OperatorUEConnections {
  operator: string;
  ue_connections: number;
}

export interface RadioIdentity {
  type: RANNodeType;
  global_enb_id?: GlobalENBID;
  global_gnb_id?: GlobalGNBID;
  name?: string;
}

export const listRadios = ({ page = 1, perPage = 25 }: PageParams = {}) =>
  apiFetch<Page<Radio>>(
    withQuery("/api/v1/radios", { page, per_page: perPage }),
  );

export const radioPath = (ref: string) => `/radios/${encodeURIComponent(ref)}`;

export const getRadio = (ref: string): Promise<Radio> =>
  apiFetch<Radio>(`/api/v1/radios/${encodeURIComponent(ref)}`);

export const radioID = (radio: RadioIdentity) =>
  radio.global_enb_id?.enb_id ?? radio.global_gnb_id?.gnb_id;

export const radioLabel = (radio: RadioIdentity, fallback: string) => {
  if (radio.name !== undefined) return radio.name;
  const id = radioID(radio);
  return id === undefined ? fallback : `${radio.type} ${id}`;
};
