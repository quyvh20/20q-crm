import { useEffect, useMemo, useState } from 'react';
import { getMe, updateProfile } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { getThemePreference, setThemePreference, type ThemePreference } from '../../lib/theme';

// Profile section (U2 My Account): the first place a user can edit their own
// name, avatar, and preferences — before this, a typo'd name at signup was
// permanent without a DB console.

const COMMON_LOCALES = ['en-US', 'en-GB', 'vi-VN', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR', 'ja-JP', 'ko-KR', 'zh-CN'];

export default function ProfileSection() {
  const { setUserProfile } = useAuth();
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [firstName, setFirstName] = useState('');
  const [lastName, setLastName] = useState('');
  const [email, setEmail] = useState('');
  const [avatarUrl, setAvatarUrl] = useState('');
  const [timezone, setTimezone] = useState('');
  const [locale, setLocale] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveMsg, setSaveMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [theme, setTheme] = useState<ThemePreference>(getThemePreference());

  const timezones = useMemo<string[]>(() => {
    try {
      return Intl.supportedValuesOf('timeZone');
    } catch {
      return [];
    }
  }, []);

  useEffect(() => {
    getMe()
      .then(({ user }) => {
        setFirstName(user.first_name);
        setLastName(user.last_name);
        setEmail(user.email);
        setAvatarUrl(user.avatar_url ?? '');
        setTimezone(user.timezone ?? '');
        setLocale(user.locale ?? '');
      })
      .catch((e) => setLoadError(e instanceof Error ? e.message : 'Failed to load your profile'))
      .finally(() => setLoading(false));
  }, []);

  const save = async () => {
    setSaving(true);
    setSaveMsg(null);
    try {
      const user = await updateProfile({
        first_name: firstName,
        last_name: lastName,
        avatar_url: avatarUrl.trim(),
        timezone: timezone.trim(),
        locale: locale.trim(),
      });
      setUserProfile({
        first_name: user.first_name,
        last_name: user.last_name,
        full_name: user.full_name,
        avatar_url: user.avatar_url,
        timezone: user.timezone,
        locale: user.locale,
      });
      setSaveMsg({ ok: true, text: 'Profile saved.' });
    } catch (e) {
      setSaveMsg({ ok: false, text: e instanceof Error ? e.message : 'Failed to save' });
    } finally {
      setSaving(false);
    }
  };

  const changeTheme = (t: ThemePreference) => {
    setTheme(t);
    setThemePreference(t);
  };

  if (loading) {
    return <div className="space-y-3">{[...Array(3)].map((_, i) => <div key={i} className="h-10 rounded-lg bg-muted/50 animate-pulse" />)}</div>;
  }
  if (loadError) {
    return <div className="rounded-md border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-400">{loadError}</div>;
  }

  const initials = `${firstName?.[0] ?? ''}${lastName?.[0] ?? ''}`.toUpperCase() || '?';
  const inputCls = 'w-full px-3 py-2 bg-background border border-border rounded-lg text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary';

  return (
    <div className="space-y-6 max-w-xl">
      <div>
        <h2 className="text-lg font-semibold">Profile</h2>
        <p className="text-sm text-muted-foreground mt-0.5">How you appear to your teammates.</p>
      </div>

      {saveMsg && (
        <div className={`rounded-md border p-3 text-sm ${saveMsg.ok ? 'border-green-500/40 bg-green-500/10 text-green-500' : 'border-red-500/40 bg-red-500/10 text-red-400'}`}>
          {saveMsg.text}
        </div>
      )}

      <div className="flex items-center gap-4">
        {avatarUrl ? (
          <img src={avatarUrl} alt="" className="h-14 w-14 rounded-full object-cover border border-border" />
        ) : (
          <div className="h-14 w-14 rounded-full bg-primary/10 flex items-center justify-center text-lg font-semibold text-primary">
            {initials}
          </div>
        )}
        <div className="flex-1">
          <label className="block text-xs font-medium text-muted-foreground mb-1">Avatar URL</label>
          <input value={avatarUrl} onChange={(e) => setAvatarUrl(e.target.value)} placeholder="https://…" className={inputCls} />
          <p className="text-xs text-muted-foreground mt-1">Paste an image link, or leave empty to use your initials. Google sign-in fills this automatically.</p>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">First name</label>
          <input value={firstName} onChange={(e) => setFirstName(e.target.value)} autoComplete="given-name" className={inputCls} />
        </div>
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">Last name</label>
          <input value={lastName} onChange={(e) => setLastName(e.target.value)} autoComplete="family-name" className={inputCls} />
        </div>
      </div>

      <div>
        <label className="block text-xs font-medium text-muted-foreground mb-1">Email</label>
        <input value={email} disabled className={`${inputCls} opacity-60 cursor-not-allowed`} />
        <p className="text-xs text-muted-foreground mt-1">Changing your sign-in email is coming in a later update.</p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">Timezone</label>
          <input list="tz-options" value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="e.g. Asia/Saigon" className={inputCls} />
          <datalist id="tz-options">
            {timezones.map((tz) => <option key={tz} value={tz} />)}
          </datalist>
          <p className="text-xs text-muted-foreground mt-1">Automations you schedule use this when they don't set their own.</p>
        </div>
        <div>
          <label className="block text-xs font-medium text-muted-foreground mb-1">Locale</label>
          <input list="locale-options" value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="e.g. en-US" className={inputCls} />
          <datalist id="locale-options">
            {COMMON_LOCALES.map((l) => <option key={l} value={l} />)}
          </datalist>
        </div>
      </div>

      <div>
        <label className="block text-xs font-medium text-muted-foreground mb-1.5">Appearance</label>
        <div className="inline-flex rounded-lg border border-border overflow-hidden">
          {(['light', 'system', 'dark'] as const).map((t) => (
            <button
              key={t}
              onClick={() => changeTheme(t)}
              className={`px-4 py-1.5 text-sm capitalize transition-colors ${theme === t ? 'bg-accent text-accent-foreground font-medium' : 'text-muted-foreground hover:bg-accent/50'}`}
            >
              {t}
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground mt-1">Saved on this device.</p>
      </div>

      <div>
        <button
          onClick={save}
          disabled={saving || !firstName.trim()}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
        >
          {saving ? 'Saving…' : 'Save profile'}
        </button>
      </div>
    </div>
  );
}
