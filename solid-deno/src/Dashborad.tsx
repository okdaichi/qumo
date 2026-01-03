import { PublishBoard } from "./publish/PublishBoard.tsx";
import { SubscribeBoard } from "./subscribe/SubscribeBoard.tsx";
import { createUsername } from "./user/user_name.ts";
import { UserController, UserProvider } from "./user/UserProvider.tsx";

export function Dashborad() {
    const {username, regenerate} = createUsername();
    return 	<>
        <div style={{ display: "flex", gap: "16px", "flex-direction": "column", "align-items": "center" }}>
            <div style={{ display: "flex", gap: "16px", "align-items": "center" }}>
                <UserController regenerate={regenerate} />
                <span>{username()}</span>
            </div>


            <UserProvider username={username}>
                <div style={{ display: "flex", gap: "16px"}}>
                    <PublishBoard />
                    <SubscribeBoard />
                </div>

            </UserProvider>
        </div>
    </>
}