import { Box } from "@mui/material";
import OneLine from "@/components/OneLine";
import SettingsTable from "@/components/SettingsTable";
import StateChip from "@/components/StateChip";
import type { BroadcastPLMN, Radio, SupportedTA } from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { formatPLMN, formatSNSSAI } from "@/utils/operators";
import {
  formatRadioID,
  formatSetupFailure,
  RADIO_STATE_HELP,
  RADIO_TYPE_HELP,
  RADIO_TYPES,
  radioStateHint,
} from "@/utils/radios";

const NONE = "—";

const formatBroadcastPLMN = ({ plmn, snssais }: BroadcastPLMN) =>
  snssais?.length
    ? `${formatPLMN(plmn)} (${snssais.map(formatSNSSAI).join(", ")})`
    : formatPLMN(plmn);

const formatSupportedTAs = (tas: SupportedTA[]) =>
  tas
    .map(
      ({ tac, broadcast_plmns }) =>
        `TAC ${tac}: ${broadcast_plmns.map(formatBroadcastPLMN).join(", ")}`,
    )
    .join("; ");

const SETUP: Record<Radio["type"], string> = {
  eNB: "S1 Setup Request",
  gNB: "NG Setup Request",
};

const NAME_IE: Record<Radio["type"], string> = {
  eNB: "eNB Name",
  gNB: "RAN Node Name",
};

export default function RadioOverview({ radio }: { radio: Radio }) {
  const setup = SETUP[radio.type];
  const ng = radio.type === "gNB";

  return (
    <SettingsTable
      label="Overview"
      rows={[
        {
          label: "Name",
          help: `The name the radio gave itself (${NAME_IE[radio.type]}, in its ${setup}).`,
          value: <OneLine title={radio.name ?? NONE} />,
        },
        {
          label: "Type",
          help: RADIO_TYPE_HELP,
          value: RADIO_TYPES[radio.type],
        },
        {
          label: ng ? "Global gNB ID" : "Global eNB ID",
          help: ng
            ? `The identity the radio sent when it connected (${setup}): its PLMN, its gNB ID and the length of that ID.`
            : `The identity the radio sent when it connected (${setup}): its PLMN, the type of its eNB ID and its eNB ID.`,
          value: <OneLine title={formatRadioID(radio) ?? NONE} />,
        },
        {
          label: "Status",
          help: RADIO_STATE_HELP,
          value: (
            <Box
              component="span"
              sx={{ display: "inline-flex", alignItems: "center", gap: 1 }}
            >
              <StateChip state={radio.state} hint={radioStateHint(radio)} />
              {radio.setup_failure && (
                <OneLine title={formatSetupFailure(radio.setup_failure)} />
              )}
            </Box>
          ),
        },
        {
          label: "Connected At",
          help: "When the radio opened its link to Ella MOCN (SCTP association).",
          value: formatDateTime(radio.connected_at, { seconds: true }),
        },
        {
          label: "Addresses",
          help: "The addresses the radio connects from.",
          value: <OneLine title={radio.tnl_addresses.join(", ")} />,
        },
        {
          label: "Supported TAs",
          help: ng
            ? "The tracking areas the radio serves (TAC), each with the networks (PLMNs) it broadcasts and their slices (S-NSSAIs)."
            : "The tracking areas the radio serves (TAC), each with the networks (PLMNs) it broadcasts.",
          value: radio.supported_tas.length ? (
            <OneLine title={formatSupportedTAs(radio.supported_tas)} />
          ) : (
            NONE
          ),
        },
        {
          label: "Default Paging DRX",
          help: "How often phones in idle mode wake up to listen for pages, in radio frames (default paging DRX).",
          value:
            radio.default_paging_drx === undefined
              ? NONE
              : String(radio.default_paging_drx),
        },
      ]}
    />
  );
}
