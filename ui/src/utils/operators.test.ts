import { describe, expect, it } from "vitest";
import { authFor } from "@/contexts/AuthContext";
import type { Operator } from "@/queries/operators";
import { RoleID } from "@/queries/users";
import {
  addressErrors,
  addressRow,
  enbIdError,
  formatGlobalENBID,
  formatGlobalGNBID,
  formatPLMN,
  formatTNLAddresses,
  gnbIdError,
  gnbIdLengthError,
  isIPAddress,
  isValidAddresses,
  isValidName,
  isValidPLMNs,
  nameError,
  operatorBody,
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
    gnb_id_length: 22,
  },
  mmes: [{ tnl_addresses: ["10.0.0.1", "10.0.0.2"] }],
  amfs: [{ tnl_addresses: ["10.0.1.1"] }, { tnl_addresses: ["2001:db8::1"] }],
};

describe("operator formatting", () => {
  it("formats PLMNs and global IDs", () => {
    expect(formatPLMN({ mcc: "001", mnc: "01" })).toBe("001/01");
    expect(formatGlobalENBID(operator.global_enb_id!)).toBe(
      "315/010 · Macro (20 bits) · 703710",
    );
    expect(
      formatGlobalENBID({
        plmn: { mcc: "001", mnc: "01" },
        enb_id_type: "short_macro",
        enb_id: 7,
      }),
    ).toBe("001/01 · Short macro (18 bits) · 7");
    expect(formatGlobalGNBID(operator.global_gnb_id!)).toBe(
      "001/01 · 74565 (22 bits)",
    );
    expect(formatTNLAddresses(operator.mmes![0]!)).toBe("10.0.0.1, 10.0.0.2");
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
    expect(gnbIdLengthError("21")).toBe("22 to 32");
    expect(gnbIdLengthError("33")).toBe("22 to 32");
    expect(gnbIdLengthError("22")).toBeUndefined();
    expect(gnbIdError("4194304", "22")).toBe("0 to 4194303");
    expect(gnbIdError("4294967295", "32")).toBeUndefined();
  });

  it("rejects bad, already added and duplicate addresses", () => {
    const existing = operator.mmes!;
    expect(
      addressErrors(
        [
          addressRow(""),
          addressRow("amf.example.com"),
          addressRow(" 10.0.0.2 "),
          addressRow("2001:db8::1"),
          addressRow("2001:db8::1"),
        ],
        existing,
      ),
    ).toEqual([
      undefined,
      "Not an IPv4 or IPv6 address",
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

describe("operatorBody", () => {
  it("keeps every field and applies one change", () => {
    expect(operatorBody(operator, { pws_operator: false })).toEqual({
      ...operator,
      pws_operator: false,
    });
    expect(operatorBody({ name: "B", pws_operator: false, plmns: [] })).toEqual(
      {
        name: "B",
        pws_operator: false,
        plmns: [],
        global_enb_id: null,
        global_gnb_id: null,
        mmes: [],
        amfs: [],
      },
    );
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
