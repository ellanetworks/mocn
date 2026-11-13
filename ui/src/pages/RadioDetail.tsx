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
  radioName,
  type OperatorUEConnections,
  type Radio,
  type SupportedTA,
} from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import { formatPLMN, operatorPath } from "@/utils/operators";
import { formatRadioID, RADIO_TYPES } from "@/utils/radios";

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

const formatSupportedTAs = (tas: SupportedTA[]) =>
  tas
    .map(
      ({ tac, broadcast_plmns }) =>
        `TAC ${tac}: ${broadcast_plmns.map(formatPLMN).join(", ")}`,
    )
    .join("; ");

const OPERATOR_COLUMNS: GridColDef<OperatorUEConnections>[] = [
  {
    field: "operator",
    headerName: "Operator",
    description:
      "An operator that owns a PLMN the radio broadcasts, or that has UE connections from it.",
    flex: 1,
    minWidth: 160,
    renderCell: ({ row }) => (
      <Link
        component={RouterLink}
        to={operatorPath(row.operator)}
        variant="body2"
        sx={{ color: "link", textDecoration: "underline" }}
      >
        {row.operator}
      </Link>
    ),
  },
  {
    field: "ue_connections",
    headerName: "UE connections",
    description:
      "The UE-associated logical S1-connections or NG-connections Ella MOCN relays from the radio to the operator, one per connected UE.",
    flex: 0.5,
    minWidth: 140,
  },
];

const SETUP: Record<Radio["interface"], string> = {
  s1: "S1 Setup Request",
  ng: "NG Setup Request",
};

function RadioSections({ radio }: { radio: Radio }) {
  const setup = SETUP[radio.interface];
  const ng = radio.interface === "ng";

  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description={`What the radio sent in its ${setup}.`}
        first
      />
      <SettingsTable
        label="Overview"
        rows={[
          {
            label: "Name",
            help: `The ${ng ? "RAN node name" : "eNB name"} the radio sent in its ${setup}.`,
            value: radioName(radio) ?? NONE,
          },
          {
            label: "Type",
            help: "eNB: a 4G radio, over S1. gNB: a 5G radio, over NG.",
            value: RADIO_TYPES[radio.interface],
          },
          {
            label: ng ? "Global gNB ID" : "Global eNB ID",
            help: ng
              ? `The identity the radio sent in its ${setup}: PLMN, gNB ID and its length.`
              : `The identity the radio sent in its ${setup}: PLMN, eNB ID type and eNB ID.`,
            value: formatRadioID(radio) ?? NONE,
          },
          {
            label: "Status",
            help: ng
              ? "Connected: SCTP association up, NG Setup not done yet. Up: NG Setup succeeded."
              : "Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded.",
            value: <StateChip state={radio.state} />,
          },
          {
            label: "Connected since",
            help: "When the radio established its SCTP association.",
            value: formatDateTime(radio.connected_since, { seconds: true }),
          },
          {
            label: "Addresses",
            help: "The SCTP addresses of the radio.",
            value: <OneLine title={radio.tnl_addresses.join(", ")} />,
          },
          {
            label: "Supported TAs",
            help: "The tracking areas the radio serves, each with the PLMNs it broadcasts.",
            value: radio.supported_tas.length ? (
              <OneLine title={formatSupportedTAs(radio.supported_tas)} />
            ) : (
              NONE
            ),
          },
          {
            label: "Paging DRX",
            help: "The default paging DRX cycle of the radio, in radio frames.",
            value:
              radio.default_paging_drx === undefined
                ? NONE
                : String(radio.default_paging_drx),
          },
        ]}
      />

      <SectionHeader
        id="ue-connections-title"
        title="UE connections by operator"
        description="The UE connections Ella MOCN relays from this radio to each operator."
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
        title={radio.data ? radioLabel(radio.data) : ref}
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
