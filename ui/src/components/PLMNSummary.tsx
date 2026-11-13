import { Box } from "@mui/material";

export default function PLMNSummary({ plmns }: { plmns: string[] }) {
  const text = plmns.join(", ");
  return (
    <Box
      component="span"
      title={text}
      sx={{
        display: "block",
        minWidth: 0,
        overflow: "hidden",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      {text}
    </Box>
  );
}
