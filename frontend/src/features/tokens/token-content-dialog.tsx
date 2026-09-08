import * as Tabs from "radix-ui/tabs";
import HardDrive from "lucide-react/dist/esm/icons/hard-drive.mjs";
import MessagesSquare from "lucide-react/dist/esm/icons/messages-square.mjs";
import { Button, Dialog } from "../../components/ui";
import { TokenConversationsPanel } from "./token-conversations-panel";
import { TokenStoragePanel } from "./token-storage-panel";

export default function TokenContentDialog({ tokenId, label, onClose }: { tokenId: string; label: string; onClose: () => void }) {
  return <Dialog open onOpenChange={(open) => { if (!open) onClose(); }} title="存储与聊天" description={label} contentClassName="w-[min(96vw,1200px)]">
    <Tabs.Root defaultValue="storage" className="min-w-0">
      <Tabs.List aria-label="账号内容" className="mb-3 flex gap-3 border-b border-app-border">
        <Tabs.Trigger value="storage" asChild>
          <Button size="sm" variant="ghost" className="!gap-1 rounded-none border-0 border-b-2 border-transparent !px-1 pb-1 data-[state=active]:border-app-primary data-[state=active]:text-app-primary"><HardDrive aria-hidden="true" className="size-3.5" />存储空间</Button>
        </Tabs.Trigger>
        <Tabs.Trigger value="conversations" asChild>
          <Button size="sm" variant="ghost" className="!gap-1 rounded-none border-0 border-b-2 border-transparent !px-1 pb-1 data-[state=active]:border-app-primary data-[state=active]:text-app-primary"><MessagesSquare aria-hidden="true" className="size-3.5" />聊天记录</Button>
        </Tabs.Trigger>
      </Tabs.List>
      <Tabs.Content value="storage" className="min-w-0 outline-none"><TokenStoragePanel key={tokenId} tokenId={tokenId} /></Tabs.Content>
      <Tabs.Content value="conversations" className="min-w-0 outline-none"><TokenConversationsPanel key={tokenId} tokenId={tokenId} /></Tabs.Content>
    </Tabs.Root>
  </Dialog>;
}
