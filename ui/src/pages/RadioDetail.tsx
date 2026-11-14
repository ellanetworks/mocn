import { Box, Link, Skeleton } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useParams } from "react-router-dom";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import SettingsTable from "@/components/SettingsTable";
import StateChip from "@/components/StateChip";
import {
  getRadio,
  radioLabel,
  type BroadcastPLMN,
  type OperatorUEConnections,
  type Radio,
  type SupportedTA,
} from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import { formatPLMN, formatSNSSAI, operatorPath } from "@/utils/operators";
import { formatRadioID, RADIO_TYPE_HELP, RADIO_TYPES } from "@/utils/radios";

export const RADIO_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

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

const OPERATOR_COLUMNS: GridColDef<OperatorUEConnections>[] = [
  {
    field: "operator",
    headerName: "Operator",
    description:
      "An operator whose network the radio broadcasts, or that has devices connected through it.",
    flex: 1,
    minWidth: 160,
    renderCell: ({ row }) => (
      <OneLine title={row.operator}>
        <Link
          component={RouterLink}
          to={operatorPath(row.operator)}
          variant="body2"
          sx={{ color: "link", textDecoration: "underline" }}
        >
          {row.operator}
        </Link>
      </OneLine>
    ),
  },
  {
    field: "ue_connections",
    headerName: "UE Connections",
    description:
      "Devices connected to the operator through this radio: one per active phone, over 4G (S1) or 5G (NG).",
    flex: 0.5,
    minWidth: 145,
    renderCell: ({ row }) => <OneLine title={String(row.ue_connections)} />,
  },
];

const SETUP: Record<Radio["type"], string> = {
  eNB: "S1 Setup Request",
  gNB: "NG Setup Request",
};

const NAME_IE: Record<Radio["type"], string> = {
  eNB: "eNB Name",
  gNB: "RAN Node Name",
};

const radioTitle = (radio: Radio) =>
  radioLabel(radio, radio.ref ?? radio.tnl_addresses.join(", "));

function RadioSections({ radio }: { radio: Radio }) {
  const setup = SETUP[radio.type];
  const ng = radio.type === "gNB";

  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description="What this radio is and whether it is up."
        first
      />
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
            help: `Up: connected and ready. Connected: link up, setup not done yet (${setup}).`,
            value: <StateChip state={radio.state} />,
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

      <SectionHeader
        id="ue-connections-title"
        title="UE Connections by Operator"
        description="The devices connected through this radio, for each operator."
      />
      <DataGrid<OperatorUEConnections>
        aria-labelledby="ue-connections-title"
        rows={radio.ue_connections_by_operator ?? []}
        columns={OPERATOR_COLUMNS}
        getRowId={(row) => row.operator}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        autoHeight
        localeText={{ noRowsLabel: "No operators." }}
        sx={gridSx}
      />
    </>
  );
}

export default function RadioDetail() {
  const ref = useParams<{ ref: string }>().ref ?? "";
  const radio = useQuery({
    queryKey: ["radios", "radio", ref],
    queryFn: () => getRadio(ref),
    refetchInterval: RADIO_REFRESH_MS,
  });

  return (
    <Box component="section" aria-labelledby="radio-title">
      <PageHeader
        id="radio-title"
        title={radio.data ? radioTitle(radio.data) : ref}
        parent={{ label: "Radios", to: "/radios" }}
      />
      <QueryAlert query={radio} subject="this radio" />
      {radio.data ? (
        <RadioSections radio={radio.data} />
      ) : (
        radio.isPending && <Skeleton variant="rounded" height={240} />
      )}
    </Box>
  );
}
