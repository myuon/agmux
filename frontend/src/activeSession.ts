// Global ref to track the currently viewed session name.
// Used to suppress notifications for the session the user is actively viewing.
let activeSessionName: string | null = null;

export function setActiveSessionName(name: string | null) {
  activeSessionName = name;
}

export function getActiveSessionName(): string | null {
  return activeSessionName;
}
