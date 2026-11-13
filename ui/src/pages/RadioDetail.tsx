import { Box, Skeleton } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import SettingsTable from "@/components/SettingsTable";
import StateChip from "@/components/StateChip";
import {
  getRadio,
  radioLabel,
  type Radio,
  type SupportedTA,
} from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import { formatGlobalENBID, formatPLMN } from "@/utils/operators";

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

const TA_COLUMNS: GridColDef<SupportedTA>[] = [
  {
    field: "tac",
    headerName: "TAC",
    description: "The tracking area code.",
    flex: 0.3,
    minWidth: 100,
  },
  {
    field: "broadcast_plmns",
    headerName: "Broadcast PLMNs",
    description: "The PLMNs the radio broadcasts in this tracking area.",
    flex: 1,
    minWidth: 200,
    renderCell: ({ row }) => (
      <OneLine title={row.broadcast_plmns.map(formatPLMN).join(", ")} />
    ),
  },
];

function RadioSections({ radio }: { radio: Radio }) {
  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description="What the radio sent in its S1 Setup Request."
        first
      />
      <SettingsTable
        label="Overview"
        rows={[
          {
            label: "Name",
            help: "The name the radio sent in its S1 Setup Request.",
            value: radio.enb_name ?? NONE,
          },
          {
            label: "Global eNB ID",
            help: "The identity the radio sent in its S1 Setup Request: PLMN, eNB ID type and eNB ID.",
            value: radio.global_enb_id
              ? formatGlobalENBID(radio.global_enb_id)
              : NONE,
          },
          {
            label: "Status",
            help: "Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded.",
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
        id="tas-title"
        title={`Supported TAs (${radio.supported_tas.length})`}
        description="The tracking areas the radio serves, each with the PLMNs it broadcasts."
      />
      <DataGrid<SupportedTA>
        aria-labelledby="tas-title"
        rows={radio.supported_tas}
        columns={TA_COLUMNS}
        getRowId={(row) => row.tac}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        autoHeight
        localeText={{ noRowsLabel: "No tracking areas." }}
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
