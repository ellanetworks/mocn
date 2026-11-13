import { Box, Link } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "react-router-dom";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import StateChip from "@/components/StateChip";
import {
  listRadios,
  radioLabel,
  radioPath,
  type Radio,
} from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import { formatGlobalENBID } from "@/utils/operators";

export const RADIOS_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-row.clickable": { cursor: "pointer" },
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

interface Row extends Radio {
  id: number;
}

const columns: GridColDef<Row>[] = [
  {
    field: "enb_name",
    headerName: "Name",
    description: "The name the radio sent in its S1 Setup Request.",
    flex: 0.8,
    minWidth: 140,
    renderCell: ({ row }) =>
      row.ref ? (
        <Link
          component={RouterLink}
          to={radioPath(row.ref)}
          variant="body2"
          sx={{ color: "link", textDecoration: "underline" }}
        >
          {radioLabel(row)}
        </Link>
      ) : (
        radioLabel(row)
      ),
  },
  {
    field: "global_enb_id",
    headerName: "Global eNB ID",
    description:
      "The identity the radio sent in its S1 Setup Request: PLMN, eNB ID type and eNB ID.",
    flex: 1,
    minWidth: 200,
    valueGetter: (_value, row) =>
      row.global_enb_id ? formatGlobalENBID(row.global_enb_id) : NONE,
  },
  {
    field: "tnl_addresses",
    headerName: "Addresses",
    description: "The SCTP addresses of the radio.",
    flex: 0.7,
    minWidth: 140,
    renderCell: ({ row }) => <OneLine title={row.tnl_addresses.join(", ")} />,
  },
  {
    field: "state",
    headerName: "Status",
    description:
      "Connected: SCTP association up, S1 Setup not done yet. Up: S1 Setup succeeded.",
    flex: 0.4,
    minWidth: 120,
    renderCell: ({ row }) => (
      <StateChip
        state={row.state}
        hint={`Since ${formatDateTime(row.connected_since, { seconds: true })}`}
      />
    ),
  },
];

export default function Radios() {
  const navigate = useNavigate();
  const radios = useQuery({
    queryKey: ["radios"],
    queryFn: listRadios,
    refetchInterval: RADIOS_REFRESH_MS,
  });

  return (
    <Box component="section" aria-labelledby="radios-title">
      <PageHeader
        id="radios-title"
        title="Radios"
        count={radios.data?.length}
        description="The eNodeBs connected to Ella MOCN over S1."
      />
      <QueryAlert query={radios} subject="radios" />
      <DataGrid<Row>
        aria-label="Radios"
        rows={(radios.data ?? []).map((radio, id) => ({ ...radio, id }))}
        columns={columns}
        loading={radios.isPending}
        onRowClick={({ row }) => row.ref && navigate(radioPath(row.ref))}
        getRowClassName={({ row }) => (row.ref ? "clickable" : "")}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        autoHeight
        localeText={{ noRowsLabel: "No radios." }}
        sx={gridSx}
      />
    </Box>
  );
}
