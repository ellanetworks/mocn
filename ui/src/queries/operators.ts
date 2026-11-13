import { apiFetch } from "@/queries/utils";

export type ENBIDType = "macro" | "short_macro" | "long_macro";

export type AnyENBIDType = ENBIDType | "home";

export type MMEState = "disconnected" | "connected" | "up" | "setup_failed";

export interface PLMN {
  mcc: string;
  mnc: string;
}

export interface GlobalENBID {
  plmn: PLMN;
  enb_id_type: ENBIDType;
  enb_id: number;
}

export interface GlobalGNBID {
  plmn: PLMN;
  gnb_id: number;
  gnb_id_length: number;
}

export const CORE_NODE_KINDS = ["MME", "AMF"] as const;

export type CoreNodeKind = (typeof CORE_NODE_KINDS)[number];

export interface CoreNode {
  tnl_addresses: string[];
}

export interface ServedGUMMEI {
  served_plmns: PLMN[];
  served_group_ids: number[];
  served_mmecs: number[];
}

export type OverloadAction =
  | "reject_non_emergency_mo_dt"
  | "reject_rrc_cr_signalling"
  | "permit_emergency_sessions_and_mobile_terminated_services_only"
  | "permit_high_priority_sessions_and_mobile_terminated_services_only"
  | "reject_delay_tolerant_access"
  | "permit_high_priority_sessions_and_exception_reporting_and_mobile_terminated_services_only"
  | "not_accept_mo_data_or_delay_tolerant_access_from_cp_ciot";

export interface GUMMEI {
  plmn: PLMN;
  mme_group_id: number;
  mme_code: number;
}

export interface Overload {
  gummei: GUMMEI;
  overload_action: OverloadAction;
  traffic_load_reduction_indication?: number;
}

export interface MMEStatus {
  state: MMEState;
  since: string;
  primary_tnl_address?: string;
  mme_name?: string;
  served_gummeis?: ServedGUMMEI[];
  relative_mme_capacity?: number;
  overload?: Overload[];
  ue_connections: number;
}

export interface MME extends CoreNode {
  status?: MMEStatus;
}

export interface Operator {
  name: string;
  pws_operator: boolean;
  plmns: PLMN[];
  global_enb_id?: GlobalENBID | null;
  global_gnb_id?: GlobalGNBID | null;
  mmes?: MME[] | null;
  amfs?: CoreNode[] | null;
  ue_connections?: number;
  ue_connections_by_radio?: RadioUEConnections[];
}

export interface RadioUEConnections {
  radio: string;
  name?: string;
  ue_connections: number;
}

const operatorUrl = (name: string) =>
  `/api/v1/operators/${encodeURIComponent(name)}`;

export const listOperators = async (): Promise<Operator[]> => {
  const { items } = await apiFetch<{ items: Operator[] }>("/api/v1/operators");
  return items;
};

export const getOperator = (name: string): Promise<Operator> =>
  apiFetch<Operator>(operatorUrl(name));

export const createOperator = (operator: Operator): Promise<Operator> =>
  apiFetch<Operator>("/api/v1/operators", { method: "POST", body: operator });

export const updateOperator = (
  name: string,
  operator: Operator,
): Promise<Operator> =>
  apiFetch<Operator>(operatorUrl(name), { method: "PUT", body: operator });

export const deleteOperator = async (name: string): Promise<void> => {
  await apiFetch(operatorUrl(name), { method: "DELETE" });
};
