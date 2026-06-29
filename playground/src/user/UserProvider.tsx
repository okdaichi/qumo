import type { Accessor, ParentProps } from "solid-js";
import { UserContext } from "./context.ts";

export function UserProvider(props: ParentProps & { username: Accessor<string> }) {
	// const username = createUsername();
	return (
		<>
			{/* <UserController regenerate={username.regenerate} /> */}
			<UserContext.Provider value={{ name: props.username }}>
				{props.children}
			</UserContext.Provider>
		</>
	);
}

export function UserController(props: { regenerate: () => void }) {
	return (
		<button
			type="button"
			onClick={() => props.regenerate()}
			title="Generate new username"
		>
			Regenerate🔄
		</button>
	);
}
