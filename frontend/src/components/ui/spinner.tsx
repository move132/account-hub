import LoaderCircle from "lucide-react/dist/esm/icons/loader-circle.mjs";
export function Spinner({ label = "加载中" }: {
  label?: string;
}) {
  return <span className="inline-flex items-center gap-1.5 text-app-subtle">
    <LoaderCircle aria-hidden="true" className="size-3.5 animate-spin text-app-primary" />
    {label}</span>;
}
