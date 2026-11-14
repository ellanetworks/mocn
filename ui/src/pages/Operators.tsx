import { useState } from "react";
import {
  Box,
  Button,
  Chip,
  IconButton,
  Link,
  Skeleton,
  Tooltip,
  useMediaQuery,
} from "@mui/material";
import { useTheme } from "@mui/material/styles";
import DeleteIcon from "@mui/icons-material/Delete";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate } from "react-router-dom";
import ConfirmDialog from "@/components/ConfirmDialog";
import { CreateOperatorDialog } from "@/components/OperatorDialogs";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import { useAuth } from "@/contexts/AuthContext";
import { errorMessage, useSnackbar } from "@/contexts/SnackbarContext";
import {
  deleteOperator,
  listOperators,
  type Operator,
} from "@/queries/operators";
import { GRID_SX } from "@/utils/layout";
import {
  coreNodesUp,
  formatPLMN,
  operatorPath,
  PWS_HELP,
} from "@/utils/operators";

export const OPERATORS_REFRESH_MS = 5000;

const NONE = "—";

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

function CoreNodesChip({ operator }: { operator: Operator }) {
  const { up, total } = coreNodesUp(operator);
  if (total === 0) {
    return <Chip label="None" size="small" variant="outlined" />;
  }
  return (
    <Chip
      label={`${up}/${total} up`}
      size="small"
      color={up === total ? "success" : up > 0 ? "warning" : "default"}
    />
  );
}

const columns = (
  onDelete: ((name: string) => void) | undefined,
): GridColDef<Operator>[] => [
  {
    field: "name",
    headerName: "Name",
    flex: 1,
    minWidth: 100,
    renderCell: ({ row }) => (
      <OneLine title={row.name}>
        <Link
          component={RouterLink}
          to={operatorPath(row.name)}
          variant="body2"
          sx={{ color: "link", textDecoration: "underline" }}
        >
          {row.name}
        </Link>
      </OneLine>
    ),
  },
  {
    field: "plmns",
    headerName: "PLMNs",
    description: "The network codes (PLMN IDs, MCC-MNC) of the operator.",
    flex: 1,
    minWidth: 140,
    renderCell: ({ row }) => (
      <OneLine title={row.plmns.map(formatPLMN).join(", ")} />
    ),
  },
  {
    field: "ue_connections",
    headerName: "UE Connections",
    description:
      "Devices connected to the operator's core: one per active phone, over 4G (S1) or 5G (NG).",
    flex: 0.5,
    minWidth: 145,
    renderCell: ({ row }) => (
      <OneLine title={String(row.ue_connections ?? NONE)} />
    ),
  },
  {
    field: "pws_operator",
    headerName: "PWS",
    description: PWS_HELP,
    width: 90,
    renderCell: ({ row }) =>
      row.pws_operator ? (
        <Chip label="PWS" color="primary" size="small" />
      ) : null,
  },
  {
    field: "core_nodes",
    headerName: "Core Network Nodes",
    description:
      "How many of the operator's MMEs and AMFs are up, out of all of them.",
    flex: 0.6,
    minWidth: 120,
    renderCell: ({ row }) => <CoreNodesChip operator={row} />,
  },
  ...(onDelete
    ? [
        {
          field: "actions",
          headerName: "Actions",
          width: 72,
          align: "right",
          headerAlign: "right",
          renderCell: ({ row }) => (
            <Tooltip title="Delete operator" arrow>
              <IconButton
                size="small"
                aria-label={`Delete ${row.name}`}
                onClick={() => onDelete(row.name)}
              >
                <DeleteIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
          ),
        } satisfies GridColDef<Operator>,
      ]
    : []),
];

const HIDDEN_ON_PHONE = {
  plmns: false,
  ue_connections: false,
  pws_operator: false,
};

function DeleteOperatorDialog({
  name,
  onClose,
}: {
  name: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const { showSnackbar } = useSnackbar();
  const remove = useMutation({
    mutationFn: () => deleteOperator(name),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["operators"] });
      showSnackbar("Operator deleted successfully.", "success");
      onClose();
    },
    onError: (error) => {
      showSnackbar(
        `Failed to delete operator: ${errorMessage(error)}`,
        "error",
      );
      onClose();
    },
  });
  return (
    <ConfirmDialog
      title="Delete Operator"
      description={`Delete ${name}? Ella MOCN disconnects from its MMEs and AMFs, and its phones lose service on the shared radios. This cannot be undone.`}
      confirmLabel="Delete"
      pending={remove.isPending}
      onConfirm={() => remove.mutate()}
      onClose={onClose}
    />
  );
}

export default function Operators() {
  const auth = useAuth();
  const navigate = useNavigate();
  const theme = useTheme();
  const phone = useMediaQuery(theme.breakpoints.down("sm"));
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);
  const editable = auth.can("operator:update");

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
          editable && (
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
      {operators.isPending && <Skeleton variant="rounded" height={120} />}
      {operators.data && (
        <DataGrid<Operator>
          aria-label="Operators"
          rows={operators.data}
          columns={columns(editable ? setDeleting : undefined)}
          columnVisibilityModel={phone ? HIDDEN_ON_PHONE : {}}
          getRowId={(row) => row.name}
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
      )}
      {deleting && (
        <DeleteOperatorDialog
          name={deleting}
          onClose={() => setDeleting(null)}
        />
      )}
      {creating && (
        <CreateOperatorDialog
          onClose={() => setCreating(false)}
          onCreated={(name) => navigate(operatorPath(name))}
        />
      )}
    </Box>
  );
}
