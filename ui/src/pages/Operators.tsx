import { useState } from "react";
import { Box, Button, Chip, Link } from "@mui/material";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useQuery } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "react-router-dom";
import { CreateOperatorDialog } from "@/components/OperatorDialogs";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import { useAuth } from "@/contexts/AuthContext";
import { listOperators, type Operator } from "@/queries/operators";
import { GRID_SX } from "@/utils/layout";
import { formatPLMN, operatorPath } from "@/utils/operators";

export const OPERATORS_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-row": { cursor: "pointer" },
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

const columns: GridColDef<Operator>[] = [
  {
    field: "name",
    headerName: "Name",
    flex: 1,
    minWidth: 180,
    renderCell: ({ row }) => (
      <Link
        component={RouterLink}
        to={operatorPath(row.name)}
        variant="body2"
        sx={{ color: "link", textDecoration: "underline" }}
      >
        {row.name}
      </Link>
    ),
  },
  {
    field: "plmns",
    headerName: "PLMNs",
    flex: 1,
    minWidth: 140,
    renderCell: ({ row }) => (
      <OneLine title={row.plmns.map(formatPLMN).join(", ")} />
    ),
  },
  {
    field: "ue_connections",
    headerName: "UE connections",
    description:
      "The UE-associated logical S1-connections Ella MOCN relays to the operator's MMEs, one per UE in ECM-CONNECTED.",
    flex: 0.5,
    minWidth: 140,
    valueGetter: (_value, row) => row.ue_connections ?? NONE,
  },
  {
    field: "pws_operator",
    headerName: "PWS",
    width: 100,
    renderCell: ({ row }) =>
      row.pws_operator ? (
        <Chip label="PWS" color="primary" size="small" />
      ) : null,
  },
];

export default function Operators() {
  const auth = useAuth();
  const navigate = useNavigate();
  const [creating, setCreating] = useState(false);

  const operators = useQuery({
    queryKey: ["operators"],
    queryFn: listOperators,
    refetchInterval: OPERATORS_REFRESH_MS,
  });

  return (
    <Box component="section" aria-labelledby="operators-title">
      <PageHeader
        id="operators-title"
        title="Operators"
        count={operators.data?.length}
        description="The operators that share the radio network, each with its PLMNs and its core network."
        action={
          auth.can("operator:update") && (
            <Button
              variant="contained"
              color="success"
              onClick={() => setCreating(true)}
            >
              Create
            </Button>
          )
        }
      />
      <QueryAlert query={operators} subject="operators" />
      <DataGrid<Operator>
        aria-label="Operators"
        rows={operators.data ?? []}
        columns={columns}
        getRowId={(row) => row.name}
        loading={operators.isPending}
        onRowClick={({ row }) => navigate(operatorPath(row.name))}
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
      {creating && (
        <CreateOperatorDialog
          onClose={() => setCreating(false)}
          onCreated={(name) => navigate(operatorPath(name))}
        />
      )}
    </Box>
  );
}
