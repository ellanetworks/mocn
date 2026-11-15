import { useMemo, useState } from "react";
import { Box, Link, Skeleton, useMediaQuery } from "@mui/material";
import { useTheme } from "@mui/material/styles";
import {
  DataGrid,
  type GridColDef,
  type GridPaginationModel,
} from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink } from "react-router-dom";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import RadioDetailsDialog from "@/components/RadioDetailsDialog";
import StateChip from "@/components/StateChip";
import {
  listRadios,
  radioLabel,
  radioPath,
  type Radio,
} from "@/queries/radios";
import { GRID_SX } from "@/utils/layout";
import {
  formatRadioID,
  RADIO_STATE_HELP,
  RADIO_TYPE_HELP,
  RADIO_TYPES,
  radioRowID,
  radioStateHint,
  sameRadio,
} from "@/utils/radios";

export const RADIOS_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

const rowLabel = (radio: Radio) =>
  radioLabel(radio, radio.ref ?? radio.tnl_addresses.join(", "));

const radioColumns = (view: (radio: Radio) => void): GridColDef<Radio>[] => [
  {
    field: "name",
    headerName: "Name",
    description:
      "The name the radio gave itself (eNB Name or RAN Node Name). An unnamed radio shows its type and ID.",
    flex: 0.8,
    minWidth: 140,
    renderCell: ({ row }) => (
      <OneLine title={rowLabel(row)}>
        {row.ref ? (
          <Link
            component={RouterLink}
            to={radioPath(row.ref)}
            variant="body2"
            sx={{ color: "link", textDecoration: "underline" }}
          >
            {rowLabel(row)}
          </Link>
        ) : (
          <Link
            component="button"
            type="button"
            variant="body2"
            onClick={() => view(row)}
            sx={{ color: "link", textDecoration: "underline" }}
          >
            {rowLabel(row)}
          </Link>
        )}
      </OneLine>
    ),
  },
  {
    field: "type",
    headerName: "Type",
    description: RADIO_TYPE_HELP,
    flex: 0.3,
    minWidth: 100,
    renderCell: ({ row }) => <OneLine title={RADIO_TYPES[row.type]} />,
  },
  {
    field: "global_id",
    headerName: "Global RAN Node ID",
    description:
      "The identity the radio sent when it connected: its PLMN, the type of its ID and its ID (Global eNB ID or Global gNB ID).",
    flex: 1,
    minWidth: 200,
    renderCell: ({ row }) => <OneLine title={formatRadioID(row) ?? NONE} />,
  },
  {
    field: "tnl_addresses",
    headerName: "Addresses",
    description: "The addresses the radio connects from.",
    flex: 0.7,
    minWidth: 140,
    renderCell: ({ row }) => <OneLine title={row.tnl_addresses.join(", ")} />,
  },
  {
    field: "ue_connections",
    headerName: "UE Connections",
    description:
      "Devices connected through this radio: one per active phone, over 4G (S1) or 5G (NG).",
    flex: 0.5,
    minWidth: 145,
    renderCell: ({ row }) => <OneLine title={String(row.ue_connections)} />,
  },
  {
    field: "state",
    headerName: "Status",
    description: RADIO_STATE_HELP,
    flex: 0.4,
    minWidth: 120,
    renderCell: ({ row }) => (
      <StateChip state={row.state} hint={radioStateHint(row)} />
    ),
  },
];

const HIDDEN_ON_PHONE = {
  type: false,
  global_id: false,
  tnl_addresses: false,
  ue_connections: false,
};

export default function Radios() {
  const theme = useTheme();
  const phone = useMediaQuery(theme.breakpoints.down("sm"));
  const [pagination, setPagination] = useState<GridPaginationModel>({
    page: 0,
    pageSize: 25,
  });
  const radios = useQuery({
    queryKey: ["radios", pagination],
    queryFn: () =>
      listRadios({ page: pagination.page + 1, perPage: pagination.pageSize }),
    placeholderData: (prev) => prev,
    refetchInterval: RADIOS_REFRESH_MS,
  });
  const [viewed, setViewed] = useState<Radio>();
  const columns = useMemo(() => radioColumns(setViewed), []);
  const shown =
    viewed && (radios.data?.items.find((r) => sameRadio(r, viewed)) ?? viewed);

  return (
    <Box component="section" aria-labelledby="radios-title">
      <PageHeader
        id="radios-title"
        title="Radios"
        count={radios.data?.total_count}
        description="The 4G and 5G radios connected to Ella MOCN."
      />
      <QueryAlert query={radios} subject="radios" />
      {radios.isPending && <Skeleton variant="rounded" height={120} />}
      {radios.data && (
        <DataGrid<Radio>
          aria-label="Radios"
          rows={radios.data.items}
          getRowId={radioRowID}
          columns={columns}
          columnVisibilityModel={phone ? HIDDEN_ON_PHONE : {}}
          paginationMode="server"
          rowCount={radios.data.total_count}
          paginationModel={pagination}
          onPaginationModelChange={setPagination}
          pageSizeOptions={[25, 50]}
          disableColumnSorting
          disableColumnFilter
          disableColumnMenu
          disableRowSelectionOnClick
          disableVirtualization
          autoHeight
          localeText={{ noRowsLabel: "No radios." }}
          sx={gridSx}
        />
      )}
      {shown && (
        <RadioDetailsDialog
          radio={shown}
          label={rowLabel(shown)}
          onClose={() => setViewed(undefined)}
        />
      )}
    </Box>
  );
}
