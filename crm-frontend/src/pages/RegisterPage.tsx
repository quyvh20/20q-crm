import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Building2, User } from 'lucide-react';
import { useAuth } from '../lib/auth';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import LegalConsent from '../components/auth/LegalConsent';
import { Button, Card, Input, Label } from '@/components/ui';
import { markTemplatePickerPending } from '../features/onboarding/templatePickerHandoff';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

// Repo convention is lucide icons, not raw emoji (which render inconsistently
// across platforms and are announced as "office building" by screen readers).
const ORG_TYPES = [
  { value: 'company', label: 'Company', Icon: Building2 },
  { value: 'personal', label: 'Personal', Icon: User },
] as const;

export default function RegisterPage() {
  useDocumentTitle('Create Account');
  const { register } = useAuth();
  const [form, setForm] = useState({
    org_name: '',
    org_type: 'company',
    email: '',
    password: '',
    first_name: '',
    last_name: '',
  });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    setForm((prev) => ({ ...prev, [e.target.name]: e.target.value }));
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);
    try {
      await register(form);
      // Signing up creates a workspace, so this IS the creation step — arm the
      // starter-template picker for the dashboard the user is about to land on.
      markTemplatePickerPending();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Registration failed');
    } finally {
      setLoading(false);
    }
  };

  const handleGoogleLogin = () => {
    window.location.href = `${API_URL}/api/auth/google`;
  };

  return (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            Guerrilla <span className="text-primary">CRM</span>
          </h1>
          <p className="text-sm text-muted-foreground mt-2">Create your account</p>
        </div>

        <Card className="p-8">
          <Button type="button" variant="outline" className="w-full" onClick={handleGoogleLogin}>
            <svg viewBox="0 0 24 24">
              <path d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92a5.06 5.06 0 0 1-2.2 3.32v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.1z" fill="#4285F4" />
              <path d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z" fill="#34A853" />
              <path d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z" fill="#FBBC05" />
              <path d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z" fill="#EA4335" />
            </svg>
            Continue with Google
          </Button>

          <div className="flex items-center my-6">
            <div className="flex-1 h-px bg-border"></div>
            <span className="px-4 text-sm text-muted-foreground">or register with email</span>
            <div className="flex-1 h-px bg-border"></div>
          </div>

          {/* role="alert" so a failed signup is ANNOUNCED — this banner replaces a
              plain div that a screen-reader user never heard, leaving them staring
              at a form that silently refused to submit. */}
          {error && (
            <div role="alert" className="mb-4 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <Label htmlFor="reg-orgname" className="mb-1.5">
                Workspace Name
              </Label>
              <Input
                id="reg-orgname"
                name="org_name"
                type="text"
                autoComplete="organization"
                value={form.org_name}
                onChange={handleChange}
                required
                placeholder="Acme Inc."
              />
            </div>

            {/* Workspace Type is a two-option radio group, not a text field: the
                old <label htmlFor="reg-orgtype"> pointed at an id that never
                existed (the controls are <button>s), so a screen reader announced
                two unlabelled buttons and clicking the label did nothing. A real
                radiogroup + aria-labelledby names the group and reports which
                option is selected. */}
            <div role="radiogroup" aria-labelledby="reg-orgtype-label">
              <span id="reg-orgtype-label" className="block text-sm font-medium text-foreground mb-1.5">
                Workspace Type
              </span>
              <div className="grid grid-cols-2 gap-3">
                {ORG_TYPES.map(({ value, label, Icon }) => {
                  const selected = form.org_type === value;
                  return (
                    <button
                      key={value}
                      type="button"
                      role="radio"
                      aria-checked={selected}
                      onClick={() => setForm(f => ({ ...f, org_type: value }))}
                      className={`flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg border text-sm font-medium transition-colors ${
                        selected
                          ? 'border-primary bg-primary/10 text-primary'
                          : 'border-input text-muted-foreground hover:bg-accent hover:text-accent-foreground'
                      }`}
                    >
                      <Icon className="w-4 h-4" aria-hidden="true" />
                      {label}
                    </button>
                  );
                })}
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <Label htmlFor="reg-firstname" className="mb-1.5">
                  First Name
                </Label>
                <Input
                  id="reg-firstname"
                  name="first_name"
                  type="text"
                  autoComplete="given-name"
                  value={form.first_name}
                  onChange={handleChange}
                  required
                  placeholder="John"
                />
              </div>
              <div>
                <Label htmlFor="reg-lastname" className="mb-1.5">
                  Last Name
                </Label>
                <Input
                  id="reg-lastname"
                  name="last_name"
                  type="text"
                  autoComplete="family-name"
                  value={form.last_name}
                  onChange={handleChange}
                  placeholder="Doe"
                />
              </div>
            </div>

            <div>
              <Label htmlFor="reg-email" className="mb-1.5">
                Email
              </Label>
              {/* autoComplete="username", not "email": this is the account
                  IDENTIFIER on a credential-creating form, and it's the token a
                  password manager looks for to bind the new-password below to an
                  account. With "email" the manager has no username to associate,
                  so it offers to save a password against nothing. */}
              <Input
                id="reg-email"
                name="email"
                type="email"
                autoComplete="username"
                value={form.email}
                onChange={handleChange}
                required
                placeholder="john@acme.com"
              />
            </div>

            <div>
              <Label htmlFor="reg-password" className="mb-1.5">
                Password
              </Label>
              <Input
                id="reg-password"
                name="password"
                type="password"
                autoComplete="new-password"
                value={form.password}
                onChange={handleChange}
                required
                minLength={8}
                placeholder="Min. 8 characters"
              />
            </div>

            <Button type="submit" disabled={loading} className="w-full">
              {loading ? 'Creating account...' : 'Create Account'}
            </Button>
          </form>

          {/* Consent (U7.6). Placed below the whole card body because BOTH paths
              above it create an account — the email form and, for a new Google
              user, "Continue with Google". */}
          <LegalConsent className="mt-6" />

          <p className="text-center text-sm text-muted-foreground mt-4">
            Already have an account?{' '}
            {/* <Link>, not <a href>: a raw anchor full-reloads the document and
                throws away the SPA's in-memory auth state. */}
            <Link to="/login" className="font-medium text-primary hover:underline">
              Sign in
            </Link>
          </p>
        </Card>

        <p className="text-center text-xs text-muted-foreground mt-6">
          © 2026 Guerrilla CRM. All rights reserved.
        </p>
      </div>
    </div>
  );
}
