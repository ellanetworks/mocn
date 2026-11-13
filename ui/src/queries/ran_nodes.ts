import type { AnyENBIDType, PLMN } from "@/queries/operators";
import { apiFetch } from "@/queries/utils";

export type RANNodeState = "connected" | "up";

export interface RANNodeGlobalENBID {
  plmn: PLMN;
  enb_id_type: AnyENBIDType;
  enb_id: number;
}

export interface SupportedTA {
  tac: number;
  broadcast_plmns: PLMN[];
}

export interface RANNode {
  global_enb_id: RANNodeGlobalENBID | null;
  enb_name?: string;
  supported_tas: SupportedTA[];
  default_paging_drx?: number;
  state: RANNodeState;
  connected_since: string;
  tnl_addresses: string[];
  last_failure_cause?: string;
}

export const listRANNodes = async (): Promise<RANNode[]> => {
  const { items } = await apiFetch<{ items: RANNode[] }>("/api/v1/ran-nodes");
  return items;
};
