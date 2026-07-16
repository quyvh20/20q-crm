import { useEffect, useMemo, useState } from 'react';
import { getMe, updateProfile } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { getThemePreference, setThemePreference, type ThemePreference } from '../../lib/theme';
import { localeOptions } from '../../lib/intlOptions';
import { Button, Input, Label, Skeleton } from '@/components/ui';

// Profile section (U2 My Account): the first place a user can edit their own
// name, avatar, and preferences — before this, a typo'd name at signup was
// permanent without a DB console.

const COMMON_LOCALES = localeOptions();

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
    return <div className="space-y-3">{[...Array(3)].map((_, i) => <Skeleton key={i} className="h-10 rounded-lg" />)}</div>;
  }
  if (loadError) {
    return <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">{loadError}</div>;
  }

  const initials = `${firstName?.[0] ?? ''}${lastName?.[0] ?? ''}`.toUpperCase() || '?';

  return (
    <div className="space-y-6 max-w-xl">
      <div>
        <h2 className="text-lg font-semibold">Profile</h2>
        <p className="text-sm text-muted-foreground mt-0.5">How you appear to your teammates.</p>
      </div>

      {saveMsg && (
        <div className={`rounded-lg border p-3 text-sm ${saveMsg.ok ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' : 'border-destructive/40 bg-destructive/10 text-destructive'}`}>
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
          <Label htmlFor="avatar-url" className="mb-1 block text-xs text-muted-foreground">Avatar URL</Label>
          <Input id="avatar-url" value={avatarUrl} onChange={(e) => setAvatarUrl(e.target.value)} placeholder="https://…" />
          <p className="text-xs text-muted-foreground mt-1">Paste an image link, or leave empty to use your initials. Google sign-in fills this automatically.</p>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <Label htmlFor="first-name" className="mb-1 block text-xs text-muted-foreground">First name</Label>
          <Input id="first-name" value={firstName} onChange={(e) => setFirstName(e.target.value)} autoComplete="given-name" />
        </div>
        <div>
          <Label htmlFor="last-name" className="mb-1 block text-xs text-muted-foreground">Last name</Label>
          <Input id="last-name" value={lastName} onChange={(e) => setLastName(e.target.value)} autoComplete="family-name" />
        </div>
      </div>

      <div>
        <Label htmlFor="email" className="mb-1 block text-xs text-muted-foreground">Email</Label>
        <Input id="email" value={email} disabled className="cursor-not-allowed opacity-60" />
        <p className="text-xs text-muted-foreground mt-1">Changing your sign-in email is coming in a later update.</p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <Label htmlFor="timezone" className="mb-1 block text-xs text-muted-foreground">Timezone</Label>
          <Input id="timezone" list="tz-options" value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="e.g. Asia/Saigon" />
          <datalist id="tz-options">
            {timezones.map((tz) => <option key={tz} value={tz} />)}
          </datalist>
          <p className="text-xs text-muted-foreground mt-1">Automations you schedule use this when they don't set their own.</p>
        </div>
        <div>
          <Label htmlFor="locale" className="mb-1 block text-xs text-muted-foreground">Locale</Label>
          <Input id="locale" list="locale-options" value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="e.g. en-US" />
          <datalist id="locale-options">
            {COMMON_LOCALES.map((l) => <option key={l.value} value={l.value}>{l.label}</option>)}
          </datalist>
        </div>
      </div>

      <div>
        <Label className="mb-1.5 block text-xs text-muted-foreground">Appearance</Label>
        <div className="inline-flex rounded-lg border border-input overflow-hidden">
          {(['light', 'system', 'dark'] as const).map((t) => (
            <button
              key={t}
              type="button"
              onClick={() => changeTheme(t)}
              className={`px-4 py-1.5 text-sm capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${theme === t ? 'bg-primary/10 text-primary font-medium' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'}`}
            >
              {t}
            </button>
          ))}
        </div>
        <p className="text-xs text-muted-foreground mt-1">Saved on this device.</p>
      </div>

      <div>
        <Button onClick={save} disabled={saving || !firstName.trim()}>
          {saving ? 'Saving…' : 'Save profile'}
        </Button>
      </div>
    </div>
  );
}
