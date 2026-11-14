import { useState } from "react";
import {
  Box,
  Button,
  IconButton,
  Link,
  Skeleton,
  Tooltip,
  useMediaQuery,
} from "@mui/material";
import { useTheme } from "@mui/material/styles";
import AddIcon from "@mui/icons-material/Add";
import DeleteIcon from "@mui/icons-material/Delete";
import EditIcon from "@mui/icons-material/Edit";
import ViewIcon from "@mui/icons-material/Visibility";
import { DataGrid, type GridColDef } from "@mui/x-data-grid";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link as RouterLink, useNavigate, useParams } from "react-router-dom";
import ConfirmDialog from "@/components/ConfirmDialog";
import CoreNodeDetailsDialog, {
  type CoreNodeDetails,
  type CoreNodeStatus,
} from "@/components/CoreNodeDetailsDialog";
import StateChip from "@/components/StateChip";
import {
  CoreNodeDialog,
  DeleteDialog,
  EditENBIdentityDialog,
  EditGNBIdentityDialog,
  EditPLMNsDialog,
  EditPWSOperatorDialog,
  RenameOperatorDialog,
} from "@/components/OperatorDialogs";
import OneLine from "@/components/OneLine";
import PageHeader from "@/components/PageHeader";
import QueryAlert from "@/components/QueryAlert";
import SectionHeader from "@/components/SectionHeader";
import SettingsTable from "@/components/SettingsTable";
import { useAuth } from "@/contexts/AuthContext";
import { errorMessage, useSnackbar } from "@/contexts/SnackbarContext";
import {
  deleteOperator,
  getOperator,
  type AMF,
  type CoreNodeKind,
  type MME,
  type Operator,
  type RadioUEConnections,
} from "@/queries/operators";
import { radioLabel, radioPath } from "@/queries/radios";
import { formatDateTime } from "@/utils/dates";
import { GRID_SX } from "@/utils/layout";
import {
  CORE_NODE_KIND_LABELS,
  coreNodeKey,
  ENB_IDENTITY_HELP,
  ENB_IDENTITY_LABEL,
  formatAddresses,
  formatAMFOverloadDetail,
  formatGlobalENBID,
  formatGlobalGNBID,
  formatMMEOverloadDetail,
  formatPLMN,
  formatPLMNSupport,
  formatServedGUAMIs,
  formatServedGUMMEIsItem,
  GNB_IDENTITY_HELP,
  GNB_IDENTITY_LABEL,
  operatorPath,
  PWS_HELP,
} from "@/utils/operators";

export const OPERATOR_REFRESH_MS = 5000;

const operatorQueryKey = (name: string) => ["operators", "operator", name];

const gridSx = {
  ...GRID_SX,
  "& .MuiDataGrid-cell": {
    ...GRID_SX["& .MuiDataGrid-cell"],
    display: "flex",
    alignItems: "center",
  },
};

const linkSx = { color: "link", textDecoration: "underline" };

type Editing =
  | { type: "name" }
  | { type: "plmns" }
  | { type: "pws" }
  | { type: "enb" }
  | { type: "gnb" }
  | { type: "add" }
  | { type: "view"; kind: CoreNodeKind; key: string }
  | { type: "edit"; kind: CoreNodeKind; key: string }
  | { type: "delete"; kind: CoreNodeKind; key: string };

interface NodeRow extends CoreNodeDetails {
  id: string;
  key: string;
}

const mmeStatus = ({ status }: MME): CoreNodeStatus | undefined =>
  status && {
    state: status.state,
    since: status.since,
    name: status.mme_name,
    primaryAddress: status.primary_tnl_address,
    servedIDs: status.served_gummeis?.length
      ? status.served_gummeis.map(formatServedGUMMEIsItem).join("; ")
      : undefined,
    capacity: status.relative_mme_capacity,
    ueConnections: status.ue_connections,
    overload: status.overload?.length
      ? formatMMEOverloadDetail(status.overload)
      : undefined,
  };

const amfStatus = ({ status }: AMF): CoreNodeStatus | undefined =>
  status && {
    state: status.state,
    since: status.since,
    name: status.amf_name,
    primaryAddress: status.primary_tnl_address,
    servedIDs: status.served_guamis?.length
      ? formatServedGUAMIs(status.served_guamis, status.unavailable_guamis)
      : undefined,
    plmnSupport: status.plmn_support?.length
      ? formatPLMNSupport(status.plmn_support)
      : undefined,
    capacity: status.relative_amf_capacity,
    ueConnections: status.ue_connections,
    overload: status.overload?.length
      ? formatAMFOverloadDetail(status.overload)
      : undefined,
  };

const NONE = "—";

const mmeRow = (node: MME) => nodeRow("MME", node, mmeStatus(node));

const amfRow = (node: AMF) => nodeRow("AMF", node, amfStatus(node));

const nodeRow = (
  kind: CoreNodeKind,
  node: MME | AMF,
  status: CoreNodeStatus | undefined,
): NodeRow => ({
  id: `${kind} ${coreNodeKey(node)}`,
  key: coreNodeKey(node),
  kind,
  addresses: node.tnl_addresses,
  status,
  label: status?.name ?? `${kind} ${node.tnl_addresses[0] ?? ""}`.trim(),
});

const HIDDEN_ON_PHONE = {
  addresses: false,
  kind: false,
  ue_connections: false,
};

function CoreNodes({
  mmes,
  amfs,
  identity,
  editable,
  onAdd,
  onView,
  onEdit,
  onDelete,
}: {
  mmes: MME[];
  amfs: AMF[];
  identity: Record<CoreNodeKind, boolean>;
  editable: boolean;
  onAdd: () => void;
  onView: (row: NodeRow) => void;
  onEdit: (row: NodeRow) => void;
  onDelete: (row: NodeRow) => void;
}) {
  const theme = useTheme();
  const phone = useMediaQuery(theme.breakpoints.down("sm"));
  const rows: NodeRow[] = [...mmes.map(mmeRow), ...amfs.map(amfRow)];
  const columns: GridColDef<NodeRow>[] = [
    {
      field: "addresses",
      headerName: "Addresses",
      description: "The addresses Ella MOCN uses to reach the node.",
      flex: 0.8,
      minWidth: 150,
      renderCell: ({ row }) => (
        <OneLine title={formatAddresses({ tnl_addresses: row.addresses })} />
      ),
    },
    {
      field: "kind",
      headerName: "Type",
      description:
        "MME: the 4G core, reached over S1. AMF: the 5G core, reached over NG.",
      flex: 0.4,
      minWidth: 100,
      renderCell: ({ row }) => (
        <OneLine title={CORE_NODE_KIND_LABELS[row.kind]} />
      ),
    },
    {
      field: "label",
      headerName: "Name",
      description: "The name the node gave itself. Open it to see its details.",
      flex: 0.8,
      minWidth: 90,
      renderCell: ({ row }) => (
        <OneLine title={row.label}>
          <Link
            component="button"
            type="button"
            variant="body2"
            onClick={() => onView(row)}
            sx={{ ...linkSx, verticalAlign: "baseline" }}
          >
            {row.label}
          </Link>
        </OneLine>
      ),
    },
    {
      field: "ue_connections",
      headerName: "UE Connections",
      description: "Devices connected through this node: one per active phone.",
      flex: 0.5,
      minWidth: 145,
      renderCell: ({ row }) => (
        <OneLine title={row.status ? String(row.status.ueConnections) : NONE} />
      ),
    },
    {
      field: "state",
      headerName: "Status",
      description:
        "Up: connected and ready. Connected: link up, setup not done yet. Disconnected: no link. Setup failed: the node refused the connection.",
      width: 124,
      renderCell: ({ row }) =>
        row.status ? (
          <StateChip
            state={row.status.state}
            hint={`Since ${formatDateTime(row.status.since, { seconds: true })}`}
          />
        ) : (
          NONE
        ),
    },
    {
      field: "actions",
      headerName: "Actions",
      width: editable ? 112 : 56,
      align: "right",
      headerAlign: "right",
      renderCell: ({ row }) => {
        const name = row.addresses.join(", ");
        return (
          <>
            <Tooltip title={`View ${row.kind}`} arrow>
              <IconButton
                size="small"
                aria-label={`View ${row.kind} ${name}`}
                onClick={() => onView(row)}
              >
                <ViewIcon fontSize="small" color="primary" />
              </IconButton>
            </Tooltip>
            {editable && (
              <>
                <Tooltip title={`Edit ${row.kind}`} arrow>
                  <IconButton
                    size="small"
                    aria-label={`Edit ${row.kind} ${name}`}
                    onClick={() => onEdit(row)}
                  >
                    <EditIcon fontSize="small" color="primary" />
                  </IconButton>
                </Tooltip>
                <Tooltip title={`Delete ${row.kind}`} arrow>
                  <IconButton
                    size="small"
                    aria-label={`Delete ${row.kind} ${name}`}
                    onClick={() => onDelete(row)}
                  >
                    <DeleteIcon fontSize="small" color="primary" />
                  </IconButton>
                </Tooltip>
              </>
            )}
          </>
        );
      },
    },
  ];
  const addable = identity.MME || identity.AMF;
  const blocked =
    "Set the 4G or 5G gateway identity first. MMEs need the Global eNB ID, AMFs the Global gNB ID.";

  return (
    <>
      <SectionHeader
        id="core-nodes-title"
        title={`Core Network Nodes (${rows.length})`}
        description="The MMEs and AMFs of this operator's core."
      >
        {editable &&
          (addable ? (
            <Button
              variant="contained"
              color="success"
              startIcon={<AddIcon />}
              onClick={onAdd}
              sx={{ flexShrink: 0 }}
            >
              Add Core Node
            </Button>
          ) : (
            <Tooltip title={blocked} arrow describeChild>
              <Box
                component="span"
                tabIndex={0}
                aria-label={`Add Core Node: ${blocked}`}
                sx={{ flexShrink: 0 }}
              >
                <Button
                  variant="contained"
                  color="success"
                  startIcon={<AddIcon />}
                  disabled
                  tabIndex={-1}
                >
                  Add Core Node
                </Button>
              </Box>
            </Tooltip>
          ))}
      </SectionHeader>
      <DataGrid<NodeRow>
        aria-labelledby="core-nodes-title"
        rows={rows}
        columns={columns}
        columnVisibilityModel={phone ? HIDDEN_ON_PHONE : {}}
        disableColumnSorting
        disableColumnFilter
        disableColumnMenu
        disableRowSelectionOnClick
        disableVirtualization
        hideFooter
        localeText={{ noRowsLabel: "No core network nodes." }}
        autoHeight
        sx={gridSx}
      />
    </>
  );
}

function RadioConnections({ rows }: { rows: RadioUEConnections[] }) {
  const columns: GridColDef<RadioUEConnections>[] = [
    {
      field: "ref",
      headerName: "Radio",
      description:
        "A radio that broadcasts one of this operator's PLMNs, or that has devices connected to it.",
      flex: 1,
      minWidth: 160,
      renderCell: ({ row }) => {
        const label = radioLabel(row, row.ref);
        return (
          <OneLine title={label}>
            <Link
              component={RouterLink}
              to={radioPath(row.ref)}
              variant="body2"
              sx={linkSx}
            >
              {label}
            </Link>
          </OneLine>
        );
      },
    },
    {
      field: "ue_connections",
      headerName: "UE Connections",
      description:
        "Devices connected to this operator through the radio: one per active phone, over 4G (S1) or 5G (NG).",
      flex: 0.5,
      minWidth: 145,
      renderCell: ({ row }) => <OneLine title={String(row.ue_connections)} />,
    },
  ];
  return (
    <DataGrid<RadioUEConnections>
      aria-labelledby="ue-connections-title"
      rows={rows}
      columns={columns}
      getRowId={(row) => row.ref}
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
  );
}

function OperatorSections({
  operator,
  editable,
  onRenamed,
}: {
  operator: Operator;
  editable: boolean;
  onRenamed: (name: string) => void;
}) {
  const [editing, setEditing] = useState<Editing | null>(null);
  const close = () => setEditing(null);
  const edit = (next: Editing) =>
    editable ? () => setEditing(next) : undefined;

  const enb = operator.global_enb_id;
  const gnb = operator.global_gnb_id;
  const { mmes, amfs } = operator;
  const nodeOf = (kind: CoreNodeKind, key: string) =>
    kind === "MME"
      ? mmes.find((n) => coreNodeKey(n) === key)
      : amfs.find((n) => coreNodeKey(n) === key);
  const rowOf = (kind: CoreNodeKind, key: string) => {
    if (kind === "MME") {
      const mme = mmes.find((n) => coreNodeKey(n) === key);
      return mme && mmeRow(mme);
    }
    const amf = amfs.find((n) => coreNodeKey(n) === key);
    return amf && amfRow(amf);
  };
  const selected =
    editing?.type === "edit" || editing?.type === "delete"
      ? nodeOf(editing.kind, editing.key)
      : undefined;
  const viewing =
    editing?.type === "view" ? rowOf(editing.kind, editing.key) : undefined;
  const notSet = (
    <Box component="span" sx={{ color: "text.secondary" }}>
      Not set
    </Box>
  );

  return (
    <>
      <SectionHeader
        id="overview-title"
        title="Overview"
        description="How Ella MOCN identifies this operator."
        first
      />
      <SettingsTable
        label="Overview"
        rows={[
          {
            label: "Name",
            help: "The name of this operator in Ella MOCN.",
            value: <OneLine title={operator.name} />,
            onEdit: edit({ type: "name" }),
          },
          {
            label: "PLMNs",
            help: "The network codes (PLMN IDs, MCC-MNC) of this operator. Phones that pick one of them are sent to this operator's core.",
            value: (
              <OneLine title={operator.plmns.map(formatPLMN).join(", ")} />
            ),
            onEdit: edit({ type: "plmns" }),
          },
          {
            label: "PWS Operator",
            help: PWS_HELP,
            value: operator.pws_operator ? "Yes" : "No",
            onEdit: edit({ type: "pws" }),
          },
          {
            label: ENB_IDENTITY_LABEL,
            help: ENB_IDENTITY_HELP,
            value: enb ? <OneLine title={formatGlobalENBID(enb)} /> : notSet,
            onEdit: edit({ type: "enb" }),
          },
          {
            label: GNB_IDENTITY_LABEL,
            help: GNB_IDENTITY_HELP,
            value: gnb ? <OneLine title={formatGlobalGNBID(gnb)} /> : notSet,
            onEdit: edit({ type: "gnb" }),
          },
        ]}
      />

      <CoreNodes
        mmes={mmes}
        amfs={amfs}
        identity={{ MME: enb !== undefined, AMF: gnb !== undefined }}
        editable={editable}
        onAdd={() => setEditing({ type: "add" })}
        onView={({ kind, key }) => setEditing({ type: "view", kind, key })}
        onEdit={({ kind, key }) => setEditing({ type: "edit", kind, key })}
        onDelete={({ kind, key }) => setEditing({ type: "delete", kind, key })}
      />

      <SectionHeader
        id="ue-connections-title"
        title="UE Connections by Radio"
        description="The devices connected to this operator through each radio."
      />
      <RadioConnections rows={operator.ue_connections_by_radio ?? []} />

      {editing?.type === "name" && (
        <RenameOperatorDialog
          operator={operator}
          onClose={close}
          onRenamed={onRenamed}
        />
      )}
      {editing?.type === "plmns" && (
        <EditPLMNsDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "pws" && (
        <EditPWSOperatorDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "enb" && (
        <EditENBIdentityDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "gnb" && (
        <EditGNBIdentityDialog operator={operator} onClose={close} />
      )}
      {editing?.type === "add" && (
        <CoreNodeDialog operator={operator} onClose={close} />
      )}
      {viewing && <CoreNodeDetailsDialog node={viewing} onClose={close} />}
      {editing?.type === "edit" && selected && (
        <CoreNodeDialog
          operator={operator}
          kind={editing.kind}
          node={selected}
          onClose={close}
        />
      )}
      {editing?.type === "delete" && selected && (
        <DeleteDialog
          operator={operator}
          title={`Delete ${editing.kind}`}
          description={`Delete ${editing.kind} ${formatAddresses(selected)}? Ella MOCN closes its ${editing.kind === "MME" ? "S1" : "NG"} association, and the phones it serves lose their connection.`}
          change={
            editing.kind === "MME"
              ? { mmes: mmes.filter((n) => coreNodeKey(n) !== editing.key) }
              : { amfs: amfs.filter((n) => coreNodeKey(n) !== editing.key) }
          }
          message={`${editing.kind} deleted successfully.`}
          failure={`Failed to delete ${editing.kind}`}
          onClose={close}
        />
      )}
    </>
  );
}

export default function OperatorDetail() {
  const name = useParams<{ name: string }>().name ?? "";
  const auth = useAuth();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { showSnackbar } = useSnackbar();
  const [deleting, setDeleting] = useState(false);

  const operator = useQuery({
    queryKey: operatorQueryKey(name),
    queryFn: () => getOperator(name),
    refetchInterval: OPERATOR_REFRESH_MS,
  });

  const remove = useMutation({
    mutationFn: () => deleteOperator(name),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: operatorQueryKey(name) });
      void queryClient.invalidateQueries({ queryKey: ["operators"] });
      showSnackbar("Operator deleted successfully.", "success");
      navigate("/operators", { replace: true });
    },
    onError: (error) => {
      setDeleting(false);
      showSnackbar(
        `Failed to delete operator: ${errorMessage(error)}`,
        "error",
      );
    },
  });

  const editable = auth.can("operator:update") && operator.data !== undefined;

  return (
    <Box component="section" aria-labelledby="operator-title">
      <PageHeader
        id="operator-title"
        title={name}
        parent={{ label: "Operators", to: "/operators" }}
        action={
          editable && (
            <Button
              variant="outlined"
              color="error"
              onClick={() => setDeleting(true)}
            >
              Delete
            </Button>
          )
        }
      />
      <QueryAlert query={operator} subject="this operator" />
      {operator.data ? (
        <OperatorSections
          operator={operator.data}
          editable={editable}
          onRenamed={(renamed) => {
            queryClient.removeQueries({ queryKey: operatorQueryKey(name) });
            navigate(operatorPath(renamed), { replace: true });
          }}
        />
      ) : (
        operator.isPending && <Skeleton variant="rounded" height={240} />
      )}
      {deleting && (
        <ConfirmDialog
          title="Delete Operator"
          description={`Delete ${name}? Ella MOCN disconnects from its MMEs and AMFs, and its phones lose service on the shared radios. This cannot be undone.`}
          confirmLabel="Delete"
          pending={remove.isPending}
          onConfirm={() => remove.mutate()}
          onClose={() => setDeleting(false)}
        />
      )}
    </Box>
  );
}
