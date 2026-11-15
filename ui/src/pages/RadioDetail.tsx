import { Box, Link, Skeleton } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useParams } from "react-router-dom";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import RadioOverview from "@/components/RadioOverview";
import {
  getRadio,
  radioLabel,
  type OperatorUEConnections,
  type Radio,
} from "@/queries/radios";
import { GRID_SX } from "@/utils/layout";
import { operatorPath } from "@/utils/operators";

export const RADIO_REFRESH_MS = 5000;

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

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

const radioTitle = (radio: Radio) =>
  radioLabel(radio, radio.ref ?? radio.tnl_addresses.join(", "));

function RadioSections({ radio }: { radio: Radio }) {
  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description="What this radio is and whether it is up."
        first
      />
      <RadioOverview radio={radio} />

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
