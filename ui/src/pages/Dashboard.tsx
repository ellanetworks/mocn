import type { ReactNode } from "react";
import {
  Box,
  Card,
  type CardProps,
  CardContent,
  CardHeader,
  Skeleton,
  Tooltip,
  Typography,
} from "@mui/material";
import Grid from "@mui/material/Grid";
import { useQuery } from "@tanstack/react-query";
import PageHeader from "@/components/PageHeader";
import { getMetrics, sumOf } from "@/queries/metrics";
import { formatDateTime } from "@/utils/dates";

export const DASHBOARD_REFRESH_MS = 5000;

const BYTE_UNITS = ["B", "KiB", "MiB", "GiB", "TiB"];

const sizeFormat = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 2,
});

export const formatBytes = (bytes: number): string => {
  let n = bytes;
  let i = 0;
  while (n >= 1024 && i < BYTE_UNITS.length - 1) {
    n /= 1024;
    i++;
  }
  return `${sizeFormat.format(n)} ${BYTE_UNITS[i]}`;
};

const headerSx = {
  backgroundColor: "backgroundSubtle",
  borderTopLeftRadius: 8,
  borderTopRightRadius: 8,
  "& .MuiCardHeader-title": { fontWeight: 600, fontSize: "1rem" },
  "& .MuiCardHeader-action": { alignSelf: "center", m: 0 },
};

interface PanelProps extends Omit<CardProps, "title"> {
  title: string;
  loading?: boolean;
  error?: boolean;
  minHeight?: number;
  children?: ReactNode;
}

function Panel({
  title,
  loading,
  error,
  minHeight = 160,
  children,
  ...cardProps
}: PanelProps) {
  return (
    <Card
      {...cardProps}
      sx={{
        height: "100%",
        display: "flex",
        flexDirection: "column",
        borderRadius: 3,
        boxShadow: 2,
      }}
    >
      <CardHeader
        title={title}
        slotProps={{ title: { component: "h3" } }}
        sx={headerSx}
      />
      <CardContent
        sx={{
          flexGrow: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          minHeight,
        }}
      >
        {loading ? (
          <Skeleton variant="rounded" width={120} height={40} />
        ) : error ? (
          <Typography variant="body2" color="error">
            Failed to load data.
          </Typography>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}

interface KpiCardProps {
  title: string;
  hint?: string;
  value?: string;
  loading?: boolean;
  error?: boolean;
}

function KpiCard({ title, hint, value, ...state }: KpiCardProps) {
  const card = (
    <Panel title={title} tabIndex={0} {...state}>
      <Typography variant="h4">{value}</Typography>
    </Panel>
  );

  return hint ? (
    <Tooltip title={hint} arrow describeChild>
      {card}
    </Tooltip>
  ) : (
    card
  );
}

function Section({
  title,
  first,
  children,
}: {
  title: string;
  first?: boolean;
  children: ReactNode;
}) {
  return (
    <Box component="section" aria-label={title}>
      <Typography variant="h5" component="h2" sx={{ mt: first ? 0 : 4, mb: 2 }}>
        {title}
      </Typography>
      <Grid container spacing={4} sx={{ alignItems: "stretch" }}>
        {children}
      </Grid>
    </Box>
  );
}

const THIRD = { xs: 12, md: 4 };

const failedToLoad = (q: { error: Error | null; data: unknown }) =>
  !!q.error && q.data === undefined;

export default function Dashboard() {
  const metrics = useQuery({
    queryKey: ["metrics"],
    queryFn: getMetrics,
    refetchInterval: DASHBOARD_REFRESH_MS,
  });

  const samples = metrics.data ?? [];
  const memory = sumOf(samples, "process_resident_memory_bytes");
  const database = sumOf(samples, "ellamocn_database_storage_bytes");
  const startedAt = sumOf(samples, "process_start_time_seconds");
  const metricCard = {
    loading: metrics.isPending,
    error: failedToLoad(metrics),
  };

  return (
    <Box component="section" aria-labelledby="dashboard-title">
      <PageHeader id="dashboard-title" title="Dashboard" />

      <Section title="System" first>
        <Grid size={THIRD}>
          <KpiCard
            title="Process Memory"
            hint="RAM used by the MOCN process (resident set size): what the operating system and a container memory limit count."
            {...metricCard}
            value={memory === undefined ? "N/A" : formatBytes(memory)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="Database Size"
            hint="Size of the database on disk, its write-ahead log included."
            {...metricCard}
            value={database === undefined ? "N/A" : formatBytes(database)}
          />
        </Grid>
        <Grid size={THIRD}>
          <KpiCard
            title="Up Since"
            hint="Time when the MOCN process was started."
            {...metricCard}
            value={
              startedAt === undefined
                ? "N/A"
                : formatDateTime(new Date(startedAt * 1000).toISOString())
            }
          />
        </Grid>
      </Section>
    </Box>
  );
}
