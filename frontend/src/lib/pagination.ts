const allowedPageSizes = new Set([10, 20, 50, 100, 200, 500]);

export function parsePageSize(value: string | number | null | undefined) {
  const size = Number(value || 20);
  return allowedPageSizes.has(size) ? size : 20;
}
