import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

export default defineConfig({
  integrations: [starlight({
    title: 'Checkride',
    description: 'Recovery drills for Kubernetes applications. Restore, inspect, and prove what survived.',
    social: [{ icon: 'github', label: 'Source code', href: 'https://github.com/jagarkarlo/checkride' }],
    sidebar: [
      { label: 'Product', items: [
        { label: 'Overview', link: '/' },
        { label: 'System design', link: '/product/' },
        { label: 'Evidence model', link: '/evidence/' },
        { label: 'Roadmap', link: '/roadmap/' },
      ] },
      { label: 'Start here', items: [
        { label: 'Overview', link: '/docs/' },
        { label: 'Run locally', slug: 'start' },
        { label: 'k3d isolated lab runbook', slug: 'guides/k3d-isolated-restore' },
        { label: 'CI/CD recovery gate', slug: 'guides/ci-recovery-gate' },
      ] },
      { label: 'Concepts', items: [
        { label: 'How Checkride works', slug: 'concepts/how-it-works' },
        { label: 'Verification levels', slug: 'levels' },
        { label: 'Write ledger & exact RPO', slug: 'concepts/write-ledger-rpo' },
        { label: 'Separate restore clusters', slug: 'concepts/isolated-restores' },
        { label: 'Comparison', slug: 'comparison' },
      ] },
      { label: 'Reference', items: [
        { label: 'Drill specification', slug: 'reference/drill-spec' },
      ] },
    ],
    customCss: ['./src/styles/theme.css'],
  })],
});