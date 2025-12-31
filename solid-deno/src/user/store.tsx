import { createSignal } from "solid-js";
import { generateUsername } from "./generator.ts";

// Global state - no Provider needed
const [username, setUsername] = createSignal(generateUsername());

const regenerate = () => {
  setUsername(generateUsername());
};

export function useUser() {
  return {
    username,
    setUsername,
    regenerate,
  };
}
