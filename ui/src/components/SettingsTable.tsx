import type { ReactNode } from "react";
import {
  IconButton,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableRow,
  Tooltip,
} from "@mui/material";
import { Edit as EditIcon } from "@mui/icons-material";
import { TABLE_CONTAINER_SX } from "@/utils/layout";

export interface SettingRow {
  label: string;
  help: string;
  value: ReactNode;
  onEdit?: () => void;
}

export default function SettingsTable({
  rows,
  label,
}: {
  rows: SettingRow[];
  label: string;
}) {
  return (
    <TableContainer sx={TABLE_CONTAINER_SX}>
      <Table aria-label={label}>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.label}>
              <TableCell sx={{ fontWeight: 600, width: "35%" }}>
                <Tooltip title={row.help} arrow describeChild>
                  <span>{row.label}</span>
                </Tooltip>
              </TableCell>
              <TableCell
                sx={{ width: "55%", maxWidth: 0, overflowWrap: "anywhere" }}
              >
                {row.value}
              </TableCell>
              <TableCell sx={{ width: "10%", textAlign: "right" }}>
                {row.onEdit && (
                  <IconButton
                    size="small"
                    onClick={row.onEdit}
                    aria-label={`Edit ${row.label}`}
                  >
                    <EditIcon fontSize="small" color="primary" />
                  </IconButton>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </TableContainer>
  );
}
