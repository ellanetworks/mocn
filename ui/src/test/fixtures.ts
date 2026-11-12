export const page = <T>(
  items: T[],
  extra: { page?: number; per_page?: number; total_count?: number } = {},
) => ({
  items,
  page: extra.page ?? 1,
  per_page: extra.per_page ?? 25,
  total_count: extra.total_count ?? items.length,
});
