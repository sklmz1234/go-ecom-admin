const SESSION_KEY = 'go-ecom-admin.session';
const RETURN_PATH_KEY = 'go-ecom-admin.return-path';

function readSession() {
  try {
    const saved = sessionStorage.getItem(SESSION_KEY);
    return saved ? JSON.parse(saved) : null;
  } catch {
    return null;
  }
}

export function getSession() {
  return readSession();
}

export function isAuthenticated() {
  return Boolean(readSession()?.token);
}

export function saveSession(session) {
  sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
}

export function clearSession() {
  sessionStorage.removeItem(SESSION_KEY);
}

export function setReturnPath(path) {
  sessionStorage.setItem(RETURN_PATH_KEY, path);
}

export function consumeReturnPath() {
  const path = sessionStorage.getItem(RETURN_PATH_KEY);
  sessionStorage.removeItem(RETURN_PATH_KEY);
  return path;
}
