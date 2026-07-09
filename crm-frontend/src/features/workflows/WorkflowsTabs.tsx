import React from 'react';
import { useNavigate } from 'react-router-dom';
import { Workflow, Mail } from 'lucide-react';

/** Tab bar shared by the /workflows area: switches between the automations list
 *  and the email-templates library (A5). Token-styled; rendered on the token-styled
 *  templates pages (the legacy dark WorkflowList renders its own dark variant). */
export const WorkflowsTabs: React.FC<{ active: 'workflows' | 'templates' }> = ({ active }) => {
  const navigate = useNavigate();
  const tab = (key: 'workflows' | 'templates', label: string, to: string, Icon: React.ComponentType<{ className?: string }>) => (
    <button
      onClick={() => navigate(to)}
      className={`inline-flex items-center gap-1.5 border-b-2 px-1 pb-2.5 text-sm font-medium transition-colors ${
        active === key
          ? 'border-primary text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground'
      }`}
    >
      <Icon className="h-4 w-4" />
      {label}
    </button>
  );

  return (
    <div className="mb-6 flex items-center gap-5 border-b border-border">
      {tab('workflows', 'Workflows', '/workflows', Workflow)}
      {tab('templates', 'Email Templates', '/workflows/email-templates', Mail)}
    </div>
  );
};

export default WorkflowsTabs;
