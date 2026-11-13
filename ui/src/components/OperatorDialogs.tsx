import { type ReactNode, useId, useState } from "react";
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
import { Add as AddIcon, Delete as DeleteIcon } from "@mui/icons-material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import ConfirmDialog from "@/components/ConfirmDialog";
import DialogError from "@/components/DialogError";
import EditDialog from "@/components/EditDialog";
import { useSnackbar } from "@/contexts/SnackbarContext";
import { useOperatorUpdate } from "@/hooks/useOperatorUpdate";
import { useTouched } from "@/hooks/useTouched";
import {
  CORE_NODE_KINDS,
  type CoreNodeKind,
  createOperator,
  type ENBIDType,
  type Operator,
  type PLMN,
} from "@/queries/operators";
import {
  addressErrors,
  addressRow,
  type AddressRow,
  ENB_ID_BITS,
  ENB_IDENTITY_LABEL,
  ENB_ID_TYPE_LABELS,
  ENB_ID_TYPES,
  enbIdError,
  formatPLMN,
  GNB_IDENTITY_LABEL,
  gnbIdError,
  gnbIdLengthError,
  isValidAddresses,
  isValidName,
  isValidPLMNs,
  MAX_GNB_ID_LENGTH,
  MAX_PLMNS,
  maxENBID,
  maxGNBID,
  MIN_GNB_ID_LENGTH,
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
          global_enb_id: null,
          global_gnb_id: null,
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

function IdentityPart({
  label,
  set,
  blocked,
  onSet,
  children,
}: {
  label: string;
  set: boolean;
  blocked?: string;
  onSet: (set: boolean) => void;
  children: ReactNode;
}) {
  const hint = useId();
  const title = useId();
  return (
    <FormControl component="fieldset" aria-labelledby={title} sx={fieldsetSx}>
      <Stack
        direction="row"
        sx={{ alignItems: "center", justifyContent: "space-between" }}
      >
        <FormLabel id={title} component="span">
          {label}
        </FormLabel>
        <Switch
          checked={set}
          onChange={(e) => onSet(e.target.checked)}
          disabled={blocked !== undefined}
          slotProps={{
            input: {
              "aria-label": "Set",
              "aria-describedby": blocked ? hint : undefined,
            },
          }}
        />
      </Stack>
      <FormHelperText id={hint} sx={{ mt: 0, minHeight: "1.66em" }}>
        {blocked ?? ""}
      </FormHelperText>
      <Stack spacing={2} sx={{ mt: 1 }}>
        {children}
      </Stack>
    </FormControl>
  );
}

export function EditGatewayIdentityDialog({
  operator,
  onClose,
}: {
  operator: Operator;
  onClose: () => void;
}) {
  const enb = operator.global_enb_id ?? undefined;
  const gnb = operator.global_gnb_id ?? undefined;
  const options = operator.plmns.map(formatPLMN);
  const initialPLMN = (plmn?: PLMN) =>
    plmn ? formatPLMN(plmn) : (options[0] ?? "");

  const [enbSet, setEnbSet] = useState(enb !== undefined);
  const [enbPLMN, setEnbPLMN] = useState(initialPLMN(enb?.plmn));
  const [enbType, setEnbType] = useState<ENBIDType>(
    enb?.enb_id_type ?? "macro",
  );
  const [enbId, setEnbId] = useState(enb ? String(enb.enb_id) : "");

  const [gnbSet, setGnbSet] = useState(gnb !== undefined);
  const [gnbPLMN, setGnbPLMN] = useState(initialPLMN(gnb?.plmn));
  const [gnbId, setGnbId] = useState(gnb ? String(gnb.gnb_id) : "");
  const [gnbLength, setGnbLength] = useState(
    String(gnb?.gnb_id_length ?? MIN_GNB_ID_LENGTH),
  );

  const mutation = useOperatorUpdate(
    operator,
    "Gateway identity saved successfully.",
    onClose,
  );

  const { touch, shown } = useTouched();
  const enbError = enbIdError(enbType, enbId);
  const enbIdShown = shown("enb.id", requiredError(enbId, enbError));
  const enbValid =
    !enbSet ||
    (options.includes(enbPLMN) && enbId !== "" && enbError === undefined);

  const gnbMax = maxGNBID(gnbLength);
  const gnbValid =
    !gnbSet ||
    (options.includes(gnbPLMN) &&
      gnbId !== "" &&
      gnbIdLengthError(gnbLength) === undefined &&
      gnbIdError(gnbId, gnbLength) === undefined);
  const gnbLengthShown = shown(
    "gnb.length",
    requiredError(gnbLength, gnbIdLengthError(gnbLength)),
  );
  const gnbIdShown = shown(
    "gnb.id",
    requiredError(gnbId, gnbIdError(gnbId, gnbLength)),
  );

  const blocked = (set: boolean, count: number, kind: string) =>
    set && count > 0 ? `Delete the ${kind}s first` : undefined;

  return (
    <EditDialog
      title="Edit Gateway Identity"
      submitLabel="Save"
      pendingLabel="Saving…"
      failure="Could not save the gateway identity"
      valid={enbValid && gnbValid}
      pending={mutation.isPending}
      error={mutation.error}
      onSubmit={() =>
        mutation.mutate({
          global_enb_id: enbSet
            ? {
                plmn: parsePLMN(enbPLMN),
                enb_id_type: enbType,
                enb_id: Number(enbId),
              }
            : null,
          global_gnb_id: gnbSet
            ? {
                plmn: parsePLMN(gnbPLMN),
                gnb_id: Number(gnbId),
                gnb_id_length: Number(gnbLength),
              }
            : null,
        })
      }
      onClose={onClose}
    >
      <IdentityPart
        label={ENB_IDENTITY_LABEL}
        set={enbSet}
        blocked={blocked(enbSet, operator.mmes?.length ?? 0, "MME")}
        onSet={setEnbSet}
      >
        <PLMNSelect
          value={enbPLMN}
          options={options}
          onChange={setEnbPLMN}
          disabled={!enbSet}
        />
        <TextField
          select
          label="eNB ID type"
          value={enbType}
          onChange={(e) => setEnbType(e.target.value as ENBIDType)}
          disabled={!enbSet}
          required
        >
          {ENB_ID_TYPES.map((t) => (
            <MenuItem key={t} value={t}>
              {`${ENB_ID_TYPE_LABELS[t]} (${ENB_ID_BITS[t]} bits)`}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          label="eNB ID"
          value={enbId}
          onChange={(e) => setEnbId(e.target.value.trim())}
          onBlur={touch("enb.id")}
          disabled={!enbSet}
          error={enbSet && enbIdShown !== undefined}
          helperText={(enbSet && enbIdShown) || `0 to ${maxENBID(enbType)}`}
          required
          slotProps={numeric}
        />
      </IdentityPart>
      <IdentityPart
        label={GNB_IDENTITY_LABEL}
        set={gnbSet}
        blocked={blocked(gnbSet, operator.amfs?.length ?? 0, "AMF")}
        onSet={setGnbSet}
      >
        <PLMNSelect
          value={gnbPLMN}
          options={options}
          onChange={setGnbPLMN}
          disabled={!gnbSet}
        />
        <TextField
          label="gNB ID"
          value={gnbId}
          onChange={(e) => setGnbId(e.target.value.trim())}
          onBlur={touch("gnb.id")}
          disabled={!gnbSet}
          error={gnbSet && gnbIdShown !== undefined}
          helperText={
            (gnbSet && gnbIdShown) ||
            (gnbMax === undefined ? " " : `0 to ${gnbMax}`)
          }
          required
          slotProps={numeric}
        />
        <TextField
          label="gNB ID length"
          value={gnbLength}
          onChange={(e) => setGnbLength(e.target.value.trim())}
          onBlur={touch("gnb.length")}
          disabled={!gnbSet}
          error={gnbSet && gnbLengthShown !== undefined}
          helperText={
            (gnbSet && gnbLengthShown) ||
            `${MIN_GNB_ID_LENGTH} to ${MAX_GNB_ID_LENGTH} bits`
          }
          required
          slotProps={numeric}
        />
      </IdentityPart>
    </EditDialog>
  );
}

const NODE_IDENTITY_MISSING: Record<CoreNodeKind, string> = {
  MME: "Set the 4G gateway identity (Global eNB ID) first",
  AMF: "Set the 5G gateway identity (Global gNB ID) first",
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
              label={kind}
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
  index,
  onClose,
}: {
  operator: Operator;
  kind?: CoreNodeKind;
  index?: number;
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
  const nodes = (kind === "MME" ? operator.mmes : operator.amfs) ?? [];
  const editing = index === undefined ? undefined : nodes[index];
  const [rows, setRows] = useState<AddressRow[]>(() =>
    editing ? editing.tnl_addresses.map(addressRow) : [addressRow()],
  );
  const existing = nodes.filter((_, i) => i !== index);
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
    index === undefined
      ? [...nodes, node]
      : nodes.map((n, i) => (i === index ? node : n));
  const unchanged =
    editing !== undefined &&
    editing.tnl_addresses.join("\n") === node.tnl_addresses.join("\n");

  return (
    <EditDialog
      title={editing ? `Edit ${kind}` : "Add Core Network Node"}
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
          TNL addresses
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

export function DeleteDialog({
  operator,
  title,
  description,
  change,
  message,
  onClose,
}: {
  operator: Operator;
  title: string;
  description: string;
  change: Partial<Operator>;
  message: string;
  onClose: () => void;
}) {
  const mutation = useOperatorUpdate(operator, message, onClose);

  return (
    <ConfirmDialog
      title={title}
      description={description}
      confirmLabel="Delete"
      pendingLabel="Deleting…"
      pending={mutation.isPending}
      onConfirm={() => mutation.mutate(change)}
      onClose={onClose}
    >
      {mutation.error && (
        <Box sx={{ mt: 2 }}>
          <DialogError error={mutation.error} prefix="Could not delete" />
        </Box>
      )}
    </ConfirmDialog>
  );
}
