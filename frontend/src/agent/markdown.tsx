import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { openURL } from "../api";

const isWeb = (href?: string) => !!href && /^https?:\/\//i.test(href);

// Model output is untrusted: raw HTML is dropped (skipHtml), links open in the
// system browser and only for http(s), and images are never fetched (a remote
// image would tell its server what the user asked about).
const components: Components = {
  a: ({ href, children }) =>
    isWeb(href) ? (
      <a href={href} title={href} onClick={(e) => { e.preventDefault(); openURL(href!); }}>{children}</a>
    ) : (
      <span>{children}</span>
    ),
  img: ({ alt }) => <span className="md-img">[图片{alt ? `：${alt}` : ""}]</span>,
};

/** Renders the agent's Markdown safely. */
export function MarkdownView({ text }: { text: string }) {
  return (
    <div className="md">
      <Markdown remarkPlugins={[remarkGfm]} skipHtml components={components}>{text}</Markdown>
    </div>
  );
}
