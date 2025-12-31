import { useUser } from "./store.tsx";

export function UserBadge() {
  const { username, regenerate } = useUser();

  return (
    <div class="user-badge">
      <span class="username">{username()}</span>
      <button type="button" onClick={regenerate} title="Generate new username">
        🔄
      </button>
    </div>
  );
}
