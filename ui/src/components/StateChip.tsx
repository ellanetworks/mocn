import { Chip, Tooltip, type ChipProps } from "@mui/material";
import type { MMEState } from "@/queries/operators";

const STATE_LABELS: Record<MMEState, string> = {
  disconnected: "Disconnected",
  connected: "Connected",
  up: "Up",
  setup_failed: "Setup failed",
};

const stateColor = (state: MMEState): ChipProps["color"] => {
  switch (state) {
    case "up":
      return "success";
    case "connected":
      return "warning";
    default:
      return "error";
  }
};

export default function StateChip({
  state,
  hint,
}: {
  state: MMEState;
  hint?: string;
}) {
  const chip = (
    <Chip
      label={STATE_LABELS[state] ?? state}
      color={stateColor(state)}
      size="small"
    />
  );

  return hint ? (
    <Tooltip title={hint} arrow describeChild>
      {chip}
    </Tooltip>
  ) : (
    chip
  );
}
