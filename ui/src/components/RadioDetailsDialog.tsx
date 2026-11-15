import {
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
} from "@mui/material";
import RadioOverview from "@/components/RadioOverview";
import type { Radio } from "@/queries/radios";

export default function RadioDetailsDialog({
  radio,
  label,
  onClose,
}: {
  radio: Radio;
  label: string;
  onClose: () => void;
}) {
  return (
    <Dialog
      open
      onClose={onClose}
      fullWidth
      maxWidth="sm"
      aria-labelledby="radio-details-title"
    >
      <DialogTitle id="radio-details-title">{label}</DialogTitle>
      <DialogContent dividers>
        <RadioOverview radio={radio} />
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}
