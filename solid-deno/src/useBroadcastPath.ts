import type { BroadcastPath } from "@qumo/moq";
import { useUser } from "./user/context.ts";

export function useBroadcastPath() {
	const user = useUser();
	const broadcastPath: BroadcastPath = `/${user.name()}`;
	return broadcastPath;
}
