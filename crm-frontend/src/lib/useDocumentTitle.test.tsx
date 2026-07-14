import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render, renderHook, cleanup } from '@testing-library/react';
import { useDocumentTitle, DocumentTitle, APP_NAME } from './useDocumentTitle';

describe('useDocumentTitle', () => {
  beforeEach(() => {
    document.title = 'initial';
  });
  afterEach(cleanup);

  it('suffixes the title with the app name', () => {
    renderHook(() => useDocumentTitle('Contacts'));
    expect(document.title).toBe(`Contacts · ${APP_NAME}`);
  });

  it('updates when the title changes', () => {
    const { rerender } = renderHook(({ t }) => useDocumentTitle(t), {
      initialProps: { t: 'Deals' },
    });
    expect(document.title).toBe(`Deals · ${APP_NAME}`);

    rerender({ t: 'Reports' });
    expect(document.title).toBe(`Reports · ${APP_NAME}`);
  });

  // The guard that matters: a detail page passes `deal?.title`, which is undefined
  // for the first render. Interpolating it blindly would show "undefined · …".
  it.each([
    ['undefined', undefined],
    ['null', null],
    ['empty', ''],
    ['whitespace', '   '],
  ])('falls back to the bare app name for a %s title', (_label, value) => {
    renderHook(() => useDocumentTitle(value as string | null | undefined));
    expect(document.title).toBe(APP_NAME);
    expect(document.title).not.toContain('undefined');
  });

  it('does not restore the previous title on unmount', () => {
    const { unmount } = renderHook(() => useDocumentTitle('Deals'));
    expect(document.title).toBe(`Deals · ${APP_NAME}`);
    unmount();
    // The NEXT route sets its own title; restoring here would leave the tab one
    // page behind.
    expect(document.title).toBe(`Deals · ${APP_NAME}`);
  });

  describe('DocumentTitle component', () => {
    it('sets the title and renders nothing', () => {
      const { container } = render(<DocumentTitle title="Settings" />);
      expect(document.title).toBe(`Settings · ${APP_NAME}`);
      expect(container).toBeEmptyDOMElement();
    });

    // These three tests pin down WHY AppLayout uses the component form rather than
    // calling the hook directly. React runs a parent component's effects AFTER its
    // children's, so a layout that called useDocumentTitle(props.title) would fire
    // last and stamp its own value — including `undefined` → the bare app name —
    // on top of whatever the page inside it had just set.
    function ChildPage() {
      useDocumentTitle('Acme Corp deal');
      return null;
    }

    it('a LAYOUT THAT CALLS THE HOOK clobbers its child page (the trap we avoid)', () => {
      function BadLayout({ title, children }: { title?: string; children: React.ReactNode }) {
        useDocumentTitle(title); // parent effect → runs last → wins
        return <div>{children}</div>;
      }
      render(
        <BadLayout>
          <ChildPage />
        </BadLayout>,
      );
      // The page set a real title, and the layout's `undefined` wiped it out.
      expect(document.title).toBe(APP_NAME);
    });

    // The shipped shape: <DocumentTitle> sits BEFORE {children} in the tree, so its
    // effect runs first and a page that sets its own title still wins.
    function Layout({ title, children }: { title?: string; children: React.ReactNode }) {
      return (
        <div>
          {title && <DocumentTitle title={title} />}
          {children}
        </div>
      );
    }

    it('titles the tab for a static page that sets nothing itself', () => {
      render(
        <Layout title="Deals">
          <div />
        </Layout>,
      );
      expect(document.title).toBe(`Deals · ${APP_NAME}`);
    });

    it('yields to a child page that owns its own (dynamic) title', () => {
      render(
        <Layout>
          <ChildPage />
        </Layout>,
      );
      expect(document.title).toBe(`Acme Corp deal · ${APP_NAME}`);
    });
  });
});
