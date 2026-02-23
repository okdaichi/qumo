import { Client, DefaultTrackMux } from "@okdaichi/moq";
import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import { createUsername } from "./user/user_name.ts";
import { UserController, UserProvider } from "./user/UserProvider.tsx";

export function Dashborad() {
	const { username, regenerate } = createUsername();
	const relayUrl = import.meta.env.VITE_RELAY_URL ?? "https://localhost:4433";

	const client = new Client();
	const mux = DefaultTrackMux;
	const session = client.dial(relayUrl, mux);
	session.catch((e) => console.error("[client] session failed:", e));
	return (
		<>
			<div
				style={{
					display: "flex",
					gap: "16px",
					"flex-direction": "column",
					"align-items": "center",
				}}
			>
				<div style={{ display: "flex", gap: "16px", "align-items": "center" }}>
					<UserController regenerate={regenerate} />
					<span>{username()}</span>
				</div>

				<UserProvider username={username}>
					<div style={{ display: "flex", gap: "16px" }}>
						<PublishBoard mux={mux} />
						<SubscribeBoard session={session} />
					</div>
				</UserProvider>
			</div>
		</>
	);
}
