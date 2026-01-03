import { type Accessor, createContext, useContext } from "solid-js";
import { generateUsername } from "./user_name.ts";

export interface User {
	name: Accessor<string>;
}

const defaultUser: User = {
	name: () => generateUsername(),
};

export const UserContext = createContext<User>(defaultUser);

export function useUser(): User {
	const ctx = useContext(UserContext);
	if (!ctx) {
		throw new Error("useUser must be used within a UserProvider");
	}
	return ctx;
}
