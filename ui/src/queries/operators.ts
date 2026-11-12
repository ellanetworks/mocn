import { apiFetch } from "@/queries/utils";

export type ENBIDType = "macro" | "home" | "short_macro" | "long_macro";

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

export interface Operator {
  name: string;
  pws_operator: boolean;
  plmns: PLMN[];
  global_enb_id?: GlobalENBID | null;
  global_gnb_id?: GlobalGNBID | null;
  mmes?: string[] | null;
  amfs?: string[] | null;
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
