import { describe, expect, it } from "vitest";
import { authFor } from "@/contexts/AuthContext";
import type { Operator } from "@/queries/operators";
import { RoleID } from "@/queries/users";
import {
  addressErrors,
  addressRow,
  canonicalAddress,
  coreNodeKey,
  coreNodesUp,
  enbIdError,
  formatAMFOverloadDetail,
  formatGlobalENBID,
  formatGlobalGNBID,
  formatGUMMEI,
  formatMMEOverloadDetail,
  formatPLMN,
  formatPLMNSupport,
  formatAddresses,
  formatServedGUAMIs,
  formatServedGUMMEIsItem,
  gnbIdBitLengthError,
  gnbIdError,
  isIPAddress,
  isValidAddresses,
  isValidName,
  isValidPLMNs,
  nameError,
  operatorBody,
  parsePLMN,
  plmnErrors,
  plmnRow,
} from "@/utils/operators";

const operator: Operator = {
  name: "Operator A",
  pws_operator: true,
  plmns: [
    { mcc: "001", mnc: "01" },
    { mcc: "315", mnc: "010" },
  ],
  global_enb_id: {
    plmn: { mcc: "315", mnc: "010" },
    enb_id_type: "macro",
    enb_id: 703710,
  },
  global_gnb_id: {
    plmn: { mcc: "001", mnc: "01" },
    gnb_id: 74565,
    bit_length: 22,
  },
  mmes: [{ tnl_addresses: ["10.0.0.1", "10.0.0.2"] }],
  amfs: [{ tnl_addresses: ["10.0.1.1"] }, { tnl_addresses: ["2001:db8::1"] }],
};

describe("operator formatting", () => {
  it("formats PLMNs and global IDs", () => {
    expect(formatPLMN({ mcc: "001", mnc: "01" })).toBe("001-01");
    expect(parsePLMN("315-010")).toEqual({ mcc: "315", mnc: "010" });
    expect(formatGlobalENBID(operator.global_enb_id!)).toBe(
      "315-010 · Macro (20 bits) · 703710",
    );
    expect(
      formatGlobalENBID({
        plmn: { mcc: "001", mnc: "01" },
        enb_id_type: "short_macro",
        enb_id: 7,
      }),
    ).toBe("001-01 · Short macro (18 bits) · 7");
    expect(
      formatGlobalENBID({
        plmn: { mcc: "001", mnc: "01" },
        enb_id_type: "home",
        enb_id: 268435455,
      }),
    ).toBe("001-01 · Home (28 bits) · 268435455");
    expect(
      formatServedGUMMEIsItem({
        served_plmns: [
          { mcc: "001", mnc: "01" },
          { mcc: "315", mnc: "010" },
        ],
        served_group_ids: [1, 32768],
        served_mmecs: [2],
      }),
    ).toBe("PLMNs 001-01, 315-010 · MME groups 1, 32768 · MME codes 2");
    expect(formatGlobalGNBID(operator.global_gnb_id!)).toBe(
      "001-01 · 74565 (22 bits)",
    );
    expect(formatAddresses(operator.mmes[0]!)).toBe("10.0.0.1, 10.0.0.2");
    expect(
      formatGUMMEI({
        plmn: { mcc: "001", mnc: "01" },
        mme_group_id: 2,
        mme_code: 1,
      }),
    ).toBe("001-01 · MME group 2 · MME code 1");
    expect(
      formatMMEOverloadDetail([
        {
          gummei: {
            plmn: { mcc: "001", mnc: "01" },
            mme_group_id: 2,
            mme_code: 1,
          },
          overload_action: "reject_rrc_cr_signalling",
        },
      ]),
    ).toBe("GUMMEI 001-01 · MME group 2 · MME code 1: Reject signalling");
  });
});

describe("isIPAddress", () => {
  it("accepts IPv4 and IPv6 addresses only", () => {
    for (const address of ["10.0.0.1", "255.255.255.255", "::1", "fd00::a"]) {
      expect(isIPAddress(address)).toBe(true);
    }
    for (const address of [
      "",
      "10.0.0",
      "10.0.0.256",
      "01.0.0.1",
      "mme.example.com",
      "fd00:::1",
      "[::1]",
      "10.0.0.1:36412",
    ]) {
      expect(isIPAddress(address)).toBe(false);
    }
  });
});

describe("canonicalAddress", () => {
  it("writes addresses the way the server stores them", () => {
    expect(canonicalAddress("10.0.0.1")).toBe("10.0.0.1");
    expect(canonicalAddress("::ffff:10.0.0.1")).toBe("10.0.0.1");
    expect(canonicalAddress("::FFFF:a00:1")).toBe("10.0.0.1");
    expect(canonicalAddress("2001:DB8:0:0::1")).toBe("2001:db8::1");
    expect(canonicalAddress("mme.example.com")).toBe("mme.example.com");
  });
});

describe("operator validation", () => {
  it("rejects bad names", () => {
    expect(nameError("x".repeat(65))).toBe("At most 64 characters");
    expect(isValidName("x".repeat(64))).toBe(true);
    expect(isValidName("  ")).toBe(false);
  });

  it("rejects bad and duplicate PLMNs", () => {
    expect(isValidPLMNs([])).toBe(false);
    expect(isValidPLMNs([plmnRow("001", "")])).toBe(false);
    expect(isValidPLMNs([plmnRow("001", "01"), plmnRow("315", "010")])).toBe(
      true,
    );
    const many = (n: number) =>
      Array.from({ length: n }, (_, i) =>
        plmnRow("001", String(i).padStart(2, "0")),
      );
    expect(isValidPLMNs(many(12))).toBe(true);
    expect(isValidPLMNs(many(13))).toBe(false);
    expect(
      plmnErrors([
        plmnRow("01", "1"),
        plmnRow("001", "01"),
        plmnRow("001", "01"),
      ]),
    ).toEqual([
      { mcc: "3 digits", mnc: "2 or 3 digits" },
      {},
      { mnc: "Duplicate PLMN" },
    ]);
  });

  it("bounds the eNB ID by its type", () => {
    expect(enbIdError("macro", "1048576")).toBe("0 to 1048575");
    expect(enbIdError("short_macro", "262144")).toBe("0 to 262143");
    expect(enbIdError("long_macro", "2097151")).toBeUndefined();
    expect(enbIdError("macro", "-1")).toBe("0 to 1048575");
    expect(enbIdError("macro", "")).toBeUndefined();
  });

  it("bounds the gNB ID by its length", () => {
    expect(gnbIdBitLengthError("21")).toBe("22 to 32");
    expect(gnbIdBitLengthError("33")).toBe("22 to 32");
    expect(gnbIdBitLengthError("22")).toBeUndefined();
    expect(gnbIdError("4194304", "22")).toBe("0 to 4194303");
    expect(gnbIdError("4294967295", "32")).toBeUndefined();
  });

  it("rejects bad, already added and duplicate addresses", () => {
    const existing = operator.mmes;
    expect(
      addressErrors(
        [
          addressRow(""),
          addressRow("amf.example.com"),
          addressRow(" 10.0.0.2 "),
          addressRow("::ffff:10.0.0.1"),
          addressRow("2001:db8::1"),
          addressRow("2001:DB8:0::1"),
        ],
        existing,
      ),
    ).toEqual([
      undefined,
      "Not an IPv4 or IPv6 address",
      "Already added",
      "Already added",
      undefined,
      "Duplicate address",
    ]);
    expect(isValidAddresses([], existing)).toBe(false);
    expect(isValidAddresses([addressRow("")], existing)).toBe(false);
    expect(
      isValidAddresses(
        [addressRow("10.0.0.3"), addressRow("2001:db8::1")],
        existing,
      ),
    ).toBe(true);
  });
});

describe("coreNodeKey", () => {
  it("identifies a core node by its canonical address set", () => {
    expect(
      coreNodeKey({ tnl_addresses: ["10.0.0.2", "::ffff:10.0.0.1"] }),
    ).toBe(coreNodeKey({ tnl_addresses: ["10.0.0.1", "10.0.0.2"] }));
    expect(coreNodeKey({ tnl_addresses: ["2001:DB8::1"] })).toBe("2001:db8::1");
  });
});

describe("operatorBody", () => {
  it("keeps every field and applies one change", () => {
    expect(operatorBody(operator, { pws_operator: false })).toEqual({
      ...operator,
      pws_operator: false,
    });
    const bare: Operator = {
      name: "B",
      pws_operator: false,
      plmns: [],
      mmes: [],
      amfs: [],
    };
    expect(operatorBody(bare)).toEqual(bare);
  });

  it("leaves out the read-only MME status and UE connections", () => {
    const withStatus: Operator = {
      ...operator,
      mmes: [
        {
          tnl_addresses: ["10.0.0.1"],
          status: {
            state: "up",
            since: "2026-11-12T10:00:00Z",
            ue_connections: 1,
          },
        },
      ],
      ue_connections: 1,
      ue_connections_by_radio: [
        { ref: "eNB:001-01:MacroeNB-00001", type: "eNB", ue_connections: 1 },
      ],
    };
    expect(operatorBody(withStatus).mmes).toEqual([
      { tnl_addresses: ["10.0.0.1"] },
    ]);
    expect(operatorBody(withStatus)).not.toHaveProperty("ue_connections");
    expect(operatorBody(withStatus)).not.toHaveProperty(
      "ue_connections_by_radio",
    );
    expect(operatorBody(operator, { mmes: withStatus.mmes }).mmes).toEqual([
      { tnl_addresses: ["10.0.0.1"] },
    ]);
  });
});

describe("AMF status formatting", () => {
  const guami = (pointer: number) => ({
    plmn: { mcc: "001", mnc: "01" },
    amf_region_id: 1,
    amf_set_id: 2,
    amf_pointer: pointer,
  });

  it("marks the unavailable GUAMIs", () => {
    expect(formatServedGUAMIs([guami(0), guami(1)], [guami(1)])).toBe(
      "001-01 · AMF region 1 · AMF set 2 · AMF pointer 0; 001-01 · AMF region 1 · AMF set 2 · AMF pointer 1 (unavailable)",
    );
  });

  it("lists the S-NSSAIs of each PLMN", () => {
    expect(
      formatPLMNSupport([
        {
          plmn: { mcc: "001", mnc: "01" },
          snssais: [{ sst: 1 }, { sst: 1, sd: "00000a" }],
        },
      ]),
    ).toBe("001-01: SST 1, SST 1 SD 00000a");
  });

  it("details each AMF overload group", () => {
    expect(
      formatAMFOverloadDetail([
        {
          guamis: [guami(0), guami(1)],
          overload_action: "reject_non_emergency_mo_dt",
          traffic_load_reduction_indication: 20,
        },
        {
          guamis: [guami(2)],
          slices: [
            {
              snssais: [{ sst: 3 }],
              overload_action: "reject_rrc_cr_signalling",
            },
          ],
        },
      ]),
    ).toBe(
      "GUAMI 001-01 · AMF region 1 · AMF set 2 · AMF pointer 0, GUAMI 001-01 · AMF region 1 · AMF set 2 · AMF pointer 1: Reject non-emergency MO data (20%); GUAMI 001-01 · AMF region 1 · AMF set 2 · AMF pointer 2: no action, slice SST 3: Reject signalling",
    );
  });
});

describe("coreNodesUp", () => {
  it("counts the MMEs and AMFs that are up", () => {
    expect(coreNodesUp(operator)).toEqual({ up: 0, total: 3 });
    expect(
      coreNodesUp({
        ...operator,
        amfs: [
          {
            tnl_addresses: ["10.0.1.1"],
            status: { state: "up", since: "", ue_connections: 0 },
          },
          {
            tnl_addresses: ["10.0.1.2"],
            status: { state: "connected", since: "", ue_connections: 0 },
          },
        ],
      }),
    ).toEqual({ up: 1, total: 3 });
  });
});

describe("operator permissions", () => {
  it("lets every role read and only Admins and Network Managers write", () => {
    const can = (role: RoleID) => {
      const auth = authFor("u@example.com", role);
      return [auth.can("operator:read"), auth.can("operator:update")];
    };
    expect(can(RoleID.Admin)).toEqual([true, true]);
    expect(can(RoleID.NetworkManager)).toEqual([true, true]);
    expect(can(RoleID.ReadOnly)).toEqual([true, false]);
  });
});
