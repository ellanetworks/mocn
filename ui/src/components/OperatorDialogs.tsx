import { useId, useState } from "react";
import {
  Box,
  Button,
  FormControl,
  FormControlLabel,
  FormHelperText,
  FormLabel,
  IconButton,
  MenuItem,
  Radio,
  RadioGroup,
  Stack,
  Switch,
  TextField,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import DeleteIcon from "@mui/icons-material/Delete";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import ConfirmDialog from "@/components/ConfirmDialog";
import EditDialog from "@/components/EditDialog";
import { errorMessage, useSnackbar } from "@/contexts/SnackbarContext";
import { useOperatorUpdate } from "@/hooks/useOperatorUpdate";
import { useTouched } from "@/hooks/useTouched";
import { getStatus } from "@/queries/status";
import {
  CORE_NODE_KINDS,
  type CoreNode,
  type CoreNodeKind,
  createOperator,
  type ENBIDType,
  type Operator,
} from "@/queries/operators";
import {
  addressErrors,
  addressRow,
  type AddressRow,
  CORE_NODE_KIND_LABELS,
  coreNodeKey,
  ENB_ID_BITS,
  ENB_ID_TYPE_LABELS,
  enbIdError,
  formatPLMN,
  gnbIdBitLengthError,
  gnbIdError,
  isValidAddresses,
  isValidName,
  isValidPLMNs,
  MAX_GNB_ID_BIT_LENGTH,
  MAX_PLMNS,
  maxENBID,
  maxGNBID,
  MIN_GNB_ID_BIT_LENGTH,
  OPERATOR_ENB_ID_TYPES,
  nameError,
  parsePLMN,
  plmnErrors,
  plmnParams,
  plmnRow,
  plmnRows,
  type PLMNRow,
  requiredError,
} from "@/utils/operators";

const fieldsetSx = { border: 0, m: 0, p: 0, minWidth: 0 };

const numeric = { htmlInput: { inputMode: "numeric" as const } };

function NameField({
  value,
  onChange,
}: {
  value: string;
  onChange: (value: string) => void;
}) {
  const { touch, shown } = useTouched();
  const error = shown("name", requiredError(value, nameError(value)));
  return (
    <TextField
      label="Name"
      value={value}
      onChange={(e) => onChange(e.target.value)}
      onBlur={touch("name")}
      error={error !== undefined}
      helperText={error}
      required
    />
  );
}

function PLMNFields({
  rows,
  onChange,
}: {
  rows: PLMNRow[];
  onChange: (rows: PLMNRow[]) => void;
}) {
  const { touch, shown } = useTouched();
  const errors = plmnErrors(rows);
  const set = (index: number, change: Partial<PLMNRow>) =>
    onChange(rows.map((row, i) => (i === index ? { ...row, ...change } : row)));

  return (
    <FormControl component="fieldset" sx={fieldsetSx}>
      <FormLabel component="legend" sx={{ mb: 1 }}>
        PLMNs
      </FormLabel>
      <Stack spacing={1.5}>
        {rows.map((row, index) => {
          const mccError = shown(
            `${row.key}.mcc`,
            requiredError(row.mcc, errors[index]?.mcc),
          );
          const mncError = shown(
            `${row.key}.mnc`,
            requiredError(row.mnc, errors[index]?.mnc),
          );
          return (
            <Stack
              key={row.key}
              direction="row"
              spacing={1}
              sx={{ alignItems: "flex-start" }}
            >
              <TextField
                label="MCC"
                value={row.mcc}
                onChange={(e) => set(index, { mcc: e.target.value.trim() })}
                onBlur={touch(`${row.key}.mcc`)}
                error={mccError !== undefined}
                helperText={mccError}
                placeholder="001"
                required
                slotProps={numeric}
                sx={{ flex: 1 }}
              />
              <TextField
                label="MNC"
                value={row.mnc}
                onChange={(e) => set(index, { mnc: e.target.value.trim() })}
                onBlur={touch(`${row.key}.mnc`)}
                error={mncError !== undefined}
                helperText={mncError}
                placeholder="01"
                required
                slotProps={numeric}
                sx={{ flex: 1 }}
              />
              <IconButton
                aria-label={`Remove PLMN ${index + 1}`}
                onClick={() => onChange(rows.filter((_, i) => i !== index))}
                disabled={rows.length === 1}
                sx={{ mt: 1 }}
              >
                <DeleteIcon fontSize="small" />
              </IconButton>
            </Stack>
          );
        })}
        <Box>
          <Button
            size="small"
            startIcon={<AddIcon />}
            onClick={() => onChange([...rows, plmnRow()])}
            disabled={rows.length >= MAX_PLMNS}
          >
            Add PLMN
          </Button>
          {rows.length >= MAX_PLMNS && (
            <FormHelperText>At most {MAX_PLMNS} PLMNs</FormHelperText>
          )}
        </Box>
      </Stack>
    </FormControl>
  );
}

function PLMNSelect({
  value,
  options,
  onChange,
  disabled,
}: {
  value: string;
  options: string[];
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  return (
    <TextField
      select
      label="PLMN"
      value={options.includes(value) ? value : ""}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled}
      required
    >
      {options.map((option) => (
        <MenuItem key={option} value={option}>
          {option}
        </MenuItem>
      ))}
    </TextField>
  );
}

export function CreateOperatorDialog({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (name: string) => void;
}) {
  const queryClient = useQueryClient();
  const { showSnackbar } = useSnackbar();
  const [name, setName] = useState("");
  const [plmns, setPLMNs] = useState<PLMNRow[]>(() => [plmnRow()]);

  const mutation = useMutation({
    mutationFn: createOperator,
    onSuccess: (_result, operator) => {
      void queryClient.invalidateQueries({ queryKey: ["operators"] });
      showSnackbar("Operator created successfully.", "success");
      onClose();
      onCreated(operator.name);
    },
  });

  return (
    <EditDialog
      title="Create Operator"
      submitLabel="Create"
      pendingLabel="Creating…"
      failure="Could not create the operator"
      valid={isValidName(name) && isValidPLMNs(plmns)}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate({
          name: name.trim(),
          pws_operator: false,
          plmns: plmnParams(plmns),
          mmes: [],
          amfs: [],
        })
      }
      onClose={onClose}
    >
      <NameField value={name} onChange={setName} />
      <PLMNFields rows={plmns} onChange={setPLMNs} />
    </EditDialog>
  );
}

export function RenameOperatorDialog({
  operator,
  onClose,
  onRenamed,
}: {
  operator: Operator;
  onClose: () => void;
  onRenamed: (name: string) => void;
}) {
  const [name, setName] = useState(operator.name);
  const mutation = useOperatorUpdate(
    operator,
    "Operator renamed successfully.",
    (saved) => {
      onClose();
      onRenamed(saved.name);
    },
  );

  return (
    <EditDialog
      title="Rename Operator"
      submitLabel="Rename"
      pendingLabel="Renaming…"
      failure="Could not rename the operator"
      valid={isValidName(name) && name.trim() !== operator.name}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() => mutation.mutate({ name: name.trim() })}
      onClose={onClose}
    >
      <NameField value={name} onChange={setName} />
    </EditDialog>
  );
}

export function EditPLMNsDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const [plmns, setPLMNs] = useState(() => plmnRows(operator.plmns));
  const mutation = useOperatorUpdate(
    operator,
    "PLMNs updated successfully.",
    onClose,
  );

  return (
    <EditDialog
      title="Edit PLMNs"
      failure="Could not update the PLMNs"
      valid={isValidPLMNs(plmns)}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() => mutation.mutate({ plmns: plmnParams(plmns) })}
      onClose={onClose}
    >
      <PLMNFields rows={plmns} onChange={setPLMNs} />
    </EditDialog>
  );
}

function IdentitySwitch({
  label,
  set,
  blocked,
  onSet,
}: {
  label: string;
  set: boolean;
  blocked?: string;
  onSet: (set: boolean) => void;
}) {
  const control = (
    <Switch
      checked={set}
      onChange={(e) => onSet(e.target.checked)}
      disabled={blocked !== undefined}
    />
  );
  return blocked ? (
    <Tooltip title={blocked} arrow describeChild>
      <Box
        component="span"
        tabIndex={0}
        aria-label={`${label}: ${blocked}`}
        sx={{ alignSelf: "flex-start" }}
      >
        <FormControlLabel control={control} label={label} disabled />
      </Box>
    </Tooltip>
  ) : (
    <FormControlLabel control={control} label={label} />
  );
}

const blockedBy = (set: boolean, count: number, kind: string) =>
  set && count > 0
    ? `Delete this operator's ${kind}s before you remove this identity.`
    : undefined;

export function EditENBIdentityDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const enb = operator.global_enb_id;
  const options = operator.plmns.map(formatPLMN);

  const [set, setSet] = useState(enb !== undefined);
  const [plmn, setPLMN] = useState(
    enb ? formatPLMN(enb.plmn) : (options[0] ?? ""),
  );
  const [type, setType] = useState<ENBIDType>(enb?.enb_id_type ?? "macro");
  const [id, setId] = useState(enb ? String(enb.enb_id) : "");

  const mutation = useOperatorUpdate(
    operator,
    "4G gateway identity saved successfully.",
    onClose,
  );

  const { touch, shown } = useTouched();
  const error = enbIdError(type, id);
  const idShown = shown("enb.id", requiredError(id, error));
  const valid = set
    ? options.includes(plmn) && id !== "" && error === undefined
    : enb !== undefined;

  return (
    <EditDialog
      title="Edit 4G Gateway Identity"
      submitLabel="Save"
      pendingLabel="Saving…"
      failure="Could not save the 4G gateway identity"
      valid={valid}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate({
          global_enb_id: set
            ? {
                plmn: parsePLMN(plmn),
                enb_id_type: type,
                enb_id: Number(id),
              }
            : undefined,
        })
      }
      onClose={onClose}
    >
      <IdentitySwitch
        label="Set a Global eNB ID"
        set={set}
        blocked={blockedBy(set, operator.mmes.length, "MME")}
        onSet={setSet}
      />
      <PLMNSelect
        value={plmn}
        options={options}
        onChange={setPLMN}
        disabled={!set}
      />
      <TextField
        select
        label="eNB ID type"
        value={type}
        onChange={(e) => setType(e.target.value as ENBIDType)}
        disabled={!set}
        required
      >
        {OPERATOR_ENB_ID_TYPES.map((t) => (
          <MenuItem key={t} value={t}>
            {`${ENB_ID_TYPE_LABELS[t]} (${ENB_ID_BITS[t]} bits)`}
          </MenuItem>
        ))}
      </TextField>
      <TextField
        label="eNB ID"
        value={id}
        onChange={(e) => setId(e.target.value.trim())}
        onBlur={touch("enb.id")}
        disabled={!set}
        error={set && idShown !== undefined}
        helperText={(set && idShown) || `0 to ${maxENBID(type)}`}
        required
        slotProps={numeric}
      />
    </EditDialog>
  );
}

export function EditGNBIdentityDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const gnb = operator.global_gnb_id;
  const options = operator.plmns.map(formatPLMN);

  const [set, setSet] = useState(gnb !== undefined);
  const [plmn, setPLMN] = useState(
    gnb ? formatPLMN(gnb.plmn) : (options[0] ?? ""),
  );
  const [id, setId] = useState(gnb ? String(gnb.gnb_id) : "");
  const [bitLength, setBitLength] = useState(
    String(gnb?.bit_length ?? MIN_GNB_ID_BIT_LENGTH),
  );

  const mutation = useOperatorUpdate(
    operator,
    "5G gateway identity saved successfully.",
    onClose,
  );

  const { touch, shown } = useTouched();
  const max = maxGNBID(bitLength);
  const valid = set
    ? options.includes(plmn) &&
      id !== "" &&
      gnbIdBitLengthError(bitLength) === undefined &&
      gnbIdError(id, bitLength) === undefined
    : gnb !== undefined;
  const bitLengthShown = shown(
    "gnb.bit_length",
    requiredError(bitLength, gnbIdBitLengthError(bitLength)),
  );
  const idShown = shown("gnb.id", requiredError(id, gnbIdError(id, bitLength)));

  return (
    <EditDialog
      title="Edit 5G Gateway Identity"
      submitLabel="Save"
      pendingLabel="Saving…"
      failure="Could not save the 5G gateway identity"
      valid={valid}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate({
          global_gnb_id: set
            ? {
                plmn: parsePLMN(plmn),
                gnb_id: Number(id),
                bit_length: Number(bitLength),
              }
            : undefined,
        })
      }
      onClose={onClose}
    >
      <IdentitySwitch
        label="Set a Global gNB ID"
        set={set}
        blocked={blockedBy(set, operator.amfs.length, "AMF")}
        onSet={setSet}
      />
      <PLMNSelect
        value={plmn}
        options={options}
        onChange={setPLMN}
        disabled={!set}
      />
      <TextField
        label="gNB ID"
        value={id}
        onChange={(e) => setId(e.target.value.trim())}
        onBlur={touch("gnb.id")}
        disabled={!set}
        error={set && idShown !== undefined}
        helperText={
          (set && idShown) || (max === undefined ? " " : `0 to ${max}`)
        }
        required
        slotProps={numeric}
      />
      <TextField
        label="gNB ID length"
        value={bitLength}
        onChange={(e) => setBitLength(e.target.value.trim())}
        onBlur={touch("gnb.bit_length")}
        disabled={!set}
        error={set && bitLengthShown !== undefined}
        helperText={
          (set && bitLengthShown) ||
          `${MIN_GNB_ID_BIT_LENGTH} to ${MAX_GNB_ID_BIT_LENGTH} bits`
        }
        required
        slotProps={numeric}
      />
    </EditDialog>
  );
}

export const NODE_IDENTITY_MISSING: Record<CoreNodeKind, string> = {
  MME: "Set the 4G gateway identity (Global eNB ID) first.",
  AMF: "Set the 5G gateway identity (Global gNB ID) first.",
};

function CoreNodeKindField({
  value,
  available,
  onChange,
}: {
  value: CoreNodeKind;
  available: Record<CoreNodeKind, boolean>;
  onChange: (kind: CoreNodeKind) => void;
}) {
  const label = useId();
  return (
    <FormControl>
      <FormLabel id={label}>Type</FormLabel>
      <RadioGroup
        row
        aria-labelledby={label}
        value={value}
        onChange={(e) => onChange(e.target.value as CoreNodeKind)}
      >
        {CORE_NODE_KINDS.map((kind) => (
          <Tooltip
            key={kind}
            title={available[kind] ? "" : NODE_IDENTITY_MISSING[kind]}
            describeChild
          >
            <FormControlLabel
              value={kind}
              control={<Radio />}
              label={CORE_NODE_KIND_LABELS[kind]}
              disabled={!available[kind]}
            />
          </Tooltip>
        ))}
      </RadioGroup>
    </FormControl>
  );
}

export function CoreNodeDialog({
  operator,
  kind: editedKind,
  node: editing,
  onClose,
}: {
  operator: Operator;
  kind?: CoreNodeKind;
  node?: CoreNode;
  onClose: () => void;
}) {
  const available: Record<CoreNodeKind, boolean> = {
    MME: Boolean(operator.global_enb_id),
    AMF: Boolean(operator.global_gnb_id),
  };
  const [chosenKind, setKind] = useState<CoreNodeKind>(
    available.MME ? "MME" : "AMF",
  );
  const kind = editedKind ?? chosenKind;
  const nodes: CoreNode[] = kind === "MME" ? operator.mmes : operator.amfs;
  const [rows, setRows] = useState<AddressRow[]>(() =>
    editing ? editing.tnl_addresses.map(addressRow) : [addressRow()],
  );
  const editedKey = editing && coreNodeKey(editing);
  const existing = nodes.filter((n) => coreNodeKey(n) !== editedKey);
  const mutation = useOperatorUpdate(
    operator,
    editing ? `${kind} updated successfully.` : `${kind} added successfully.`,
    onClose,
  );

  const { touch, shown } = useTouched();
  const errors = addressErrors(rows, existing);
  const set = (i: number, address: string) =>
    setRows(rows.map((row, j) => (j === i ? { ...row, address } : row)));
  const node = { tnl_addresses: rows.map((row) => row.address.trim()) };
  const next =
    editedKey === undefined
      ? [...nodes, node]
      : nodes.map((n) => (coreNodeKey(n) === editedKey ? node : n));
  const unchanged =
    editing !== undefined &&
    editing.tnl_addresses.join("\n") === node.tnl_addresses.join("\n");

  return (
    <EditDialog
      title={editing ? `Edit ${kind}` : "Add Core Node"}
      {...(editing ? {} : { submitLabel: "Add", pendingLabel: "Adding…" })}
      failure={`Could not ${editing ? "update" : "add"} the ${kind}`}
      valid={!unchanged && available[kind] && isValidAddresses(rows, existing)}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate(kind === "MME" ? { mmes: next } : { amfs: next })
      }
      onClose={onClose}
    >
      {!editing && (
        <CoreNodeKindField
          value={kind}
          available={available}
          onChange={setKind}
        />
      )}
      <FormControl component="fieldset" sx={fieldsetSx}>
        <FormLabel component="legend" sx={{ mb: 1 }}>
          Addresses
        </FormLabel>
        <Stack spacing={1.5}>
          {rows.map((row, i) => {
            const error = shown(
              String(row.key),
              requiredError(row.address, errors[i]),
            );
            return (
              <Stack
                key={row.key}
                direction="row"
                spacing={1}
                sx={{ alignItems: "flex-start" }}
              >
                <TextField
                  label={`Address ${i + 1}`}
                  value={row.address}
                  onChange={(e) => set(i, e.target.value)}
                  onBlur={touch(String(row.key))}
                  error={error !== undefined}
                  helperText={error ?? "IPv4 or IPv6 address"}
                  required
                  sx={{ flex: 1 }}
                />
                <IconButton
                  aria-label={`Remove address ${i + 1}`}
                  onClick={() => setRows(rows.filter((_, j) => j !== i))}
                  disabled={rows.length === 1}
                  sx={{ mt: 1 }}
                >
                  <DeleteIcon fontSize="small" />
                </IconButton>
              </Stack>
            );
          })}
          <Box>
            <Button
              size="small"
              startIcon={<AddIcon />}
              onClick={() => setRows([...rows, addressRow()])}
            >
              Add address
            </Button>
          </Box>
        </Stack>
      </FormControl>
    </EditDialog>
  );
}

export function EditPWSOperatorDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const [checked, setChecked] = useState(operator.pws_operator);
  const mutation = useOperatorUpdate(
    operator,
    "PWS operator updated successfully.",
    onClose,
  );

  return (
    <EditDialog
      title="Edit PWS Operator"
      failure="Could not update the PWS operator"
      valid={checked !== operator.pws_operator}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() => mutation.mutate({ pws_operator: checked })}
      onClose={onClose}
    >
      <FormControlLabel
        control={
          <Switch
            checked={checked}
            onChange={(e) => setChecked(e.target.checked)}
          />
        }
        label="PWS operator"
      />
    </EditDialog>
  );
}

export function EditUserPlaneDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const current = operator.user_plane;
  const { data: status } = useQuery({
    queryKey: ["status"],
    queryFn: getStatus,
    staleTime: Infinity,
  });
  const available = status?.user_plane_aggregation ?? false;
  const [aggregation, setAggregation] = useState(current?.aggregation ?? true);
  const mutation = useOperatorUpdate(
    operator,
    "User plane saved successfully.",
    onClose,
  );

  return (
    <EditDialog
      title="Edit User Plane"
      submitLabel="Save"
      pendingLabel="Saving…"
      failure="Could not save the user plane"
      valid
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate({
          user_plane: { aggregation: available && aggregation },
        })
      }
      onClose={onClose}
    >
      <Tooltip
        title={
          available
            ? ""
            : "User-plane aggregation is not enabled on this Ella MOCN."
        }
      >
        <span>
          <FormControlLabel
            disabled={!available}
            control={
              <Switch
                checked={available && aggregation}
                onChange={(e) => setAggregation(e.target.checked)}
              />
            }
            label="User-plane aggregation"
          />
        </span>
      </Tooltip>
    </EditDialog>
  );
}

export function DeleteDialog({
  operator,
  title,
  description,
  change,
  message,
  failure,
  onClose,
}: {
  operator: Operator;
  title: string;
  description: string;
  change: Partial<Operator>;
  message: string;
  failure: string;
  onClose: () => void;
}) {
  const { showSnackbar } = useSnackbar();
  const mutation = useOperatorUpdate(operator, message, onClose);

  return (
    <ConfirmDialog
      title={title}
      description={description}
      confirmLabel="Delete"
      pendingLabel="Deleting…"
      pending={mutation.isPending}
      onConfirm={() =>
        mutation.mutate(change, {
          onError: (error) => {
            showSnackbar(`${failure}: ${errorMessage(error)}`, "error");
            onClose();
          },
        })
      }
      onClose={onClose}
    />
  );
}
