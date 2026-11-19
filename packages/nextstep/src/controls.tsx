import { useId } from 'react';
import type {
  ButtonHTMLAttributes,
  ComponentPropsWithRef,
  InputHTMLAttributes,
  ReactNode,
  SelectHTMLAttributes,
} from 'react';
import type { IconName } from './types';

/** Original 32-cell object drawings with single-cell outlines and bevel highlights. */
export function PixelIcon({
  name,
  size = 32,
  className = '',
}: {
  name: IconName;
  size?: number;
  className?: string;
}) {
  const common = {
    stroke: '#262626',
    strokeWidth: 1,
    strokeLinejoin: 'miter' as const,
  };
  return (
    <svg
      className={`ns-pixel-icon ${className}`}
      width={size}
      height={size}
      viewBox="0 0 32 32"
      aria-hidden="true"
      shapeRendering="crispEdges"
    >
      {name === 'companion' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M8 29h17v2H8z" />
          <path
            fill="#ccc"
            d="M11.5 4.5h9v2h4v3h3v13h-3v7h-5v-4h-7v4h-5v-7h-3v-13h3v-3h4z"
          />
          <path
            fill="#f4f4f4"
            stroke="none"
            d="M12 5h8v2h4v3h2v3h-3v-2h-3V9h-8v2H9v10H6V10h2V7h4z"
          />
          <path
            fill="#999"
            stroke="none"
            d="M25 13h2v9h-3v7h-4v-5h-8v5H8v-6h3v-2h12v-2h2z"
          />
          <path fill="#262626" stroke="none" d="M10 13h3v5h-3zM19 13h3v5h-3z" />
          <path fill="#eee" stroke="none" d="M10 13h1v1h-1zM19 13h1v1h-1z" />
          <path fill="none" stroke="#555" d="M14 20.5h4" />
          <path fill="none" stroke="#eee" d="M8 26h3M20 26h3" />
        </g>
      ) : name === 'terminal' ? (
        <g {...common}>
          <path fill="#aaa" d="M2.5 3.5h27v25h-27z" />
          <path fill="#eee" stroke="none" d="M3 4h26v2H3zM3 6h2v21H3z" />
          <path fill="#151918" d="M6.5 7.5h19v16h-19z" />
          <path
            fill="none"
            stroke="#eee"
            strokeWidth="2"
            d="m10 12 4 3-4 3m7 1h5"
          />
          <path fill="none" stroke="#555" d="M5 27h23V6" />
        </g>
      ) : name === 'guest-terminal' ? (
        <g {...common}>
          <path fill="#697772" d="M3.5 25.5h25l2 3v2h-29v-2z" />
          <path fill="#80a498" d="M2.5 3.5h27v22h-27z" />
          <path stroke="#cce0d8" fill="none" d="M4 23V5h24" />
          <path stroke="#48665e" fill="none" d="M4 24h24V6" />
          <path fill="#20362f" d="M6.5 7.5h19v14h-19z" />
          <path
            stroke="#d7eee0"
            strokeWidth="2"
            fill="none"
            d="m10 11 4 3-4 3m7 1h5"
          />
          <path stroke="#cad6d1" fill="none" d="M4 28h24" />
          <path fill="#374942" stroke="none" d="M21 28h6v2h-6z" />
        </g>
      ) : name === 'inspector' ? (
        <g {...common}>
          <path fill="#ccc" d="M3.5 2.5h21v25h-21z" />
          <path fill="#444" d="M4 3h20v5H4z" />
          <path fill="none" stroke="#777" d="M7 12h13M7 16h9M7 20h10" />
          <circle cx="23" cy="22" r="6" fill="#e3eee9" />
          <path strokeWidth="3" d="m27 26 4 5" />
        </g>
      ) : name === 'workspace' ? (
        <g {...common}>
          <path fill="#aaa" d="M2.5 2.5h27v27h-27z" />
          <path fill="#555" d="M6.5 6.5h8v8h-8z" />
          <path
            fill="#eee"
            d="M18.5 6.5h7v8h-7zM6.5 18.5h8v7h-8zM18.5 18.5h7v7h-7z"
          />
        </g>
      ) : name === 'clock' ? (
        <g {...common}>
          <circle cx="16" cy="16" r="14" fill="#ddd" />
          <circle cx="16" cy="16" r="11" fill="#fff" />
          <path fill="none" strokeWidth="2" d="M16 7v9l7 4" />
        </g>
      ) : name === 'folder' ? (
        <g {...common}>
          <path fill="#a59c7b" d="M2.5 8.5h10l3-3h10l3 4v18h-26z" />
          <path fill="#e3d6a8" d="M2.5 12.5h27l-2 15h-25z" />
          <path
            fill="none"
            stroke="#fff4cd"
            d="M4 14h24M4 15v11M4 10h9l3-3h9"
          />
          <path fill="none" stroke="#9c8d5e" d="M6 26h20l2-11" />
          <path fill="none" stroke="#d0c18d" d="M4 17h24M5 19h22" />
        </g>
      ) : name === 'project' ? (
        <g {...common}>
          <path fill="#a59c7b" d="M2.5 7.5h9l3-3h11l3 4v17h-26z" />
          <path fill="#e3d6a8" d="M2.5 11.5h26l-2 14h-24z" />
          <path fill="none" stroke="#fff4cd" d="M4 13h23M4 14v10M4 9h8l3-3h9" />
          <path fill="#aaa" d="M8.5 13.5h22v17h-22z" />
          <path fill="none" stroke="#eee" d="M9 14h20M9 15v14" />
          <path fill="#24322d" d="M11.5 16.5h16v11h-16z" />
          <path
            fill="none"
            stroke="#e2e7dc"
            d="m16 19-3 3 3 3m7-6 3 3-3 3m-5 1 3-8"
          />
          <path fill="none" stroke="#555" d="M10 30h20V15" />
        </g>
      ) : name === 'trash' ? (
        <g {...common}>
          <path fill="#a7a7a7" d="M7.5 7.5h19l-2 23h-15z" />
          <path fill="#ddd" d="M8.5 8.5h17l-2 21h-13z" />
          <path fill="#ddd" d="M5.5 4.5h23v4h-23zM12.5 1.5h9v3h-9z" />
          <path
            stroke="#fff"
            fill="none"
            d="M7 6h20M10 10l1 18M14 11v15M19 11v15M23 10l-1 18"
          />
          <path stroke="#777" fill="none" d="M13 11v15M18 11v15M22 11l-1 15" />
          <path stroke="#aaa" d="M12 28h10" />
        </g>
      ) : name === 'computer' ? (
        <g {...common}>
          <path fill="#bcbcbc" d="M5.5 1.5h21v22h-21zM3.5 27.5h25v3h-25z" />
          <path fill="#eee" d="M6.5 2.5h19v19h-19zM12.5 23.5h7v4h-7z" />
          <path fill="#666" d="M8.5 4.5h15v13h-15z" />
          <path fill="#657e79" stroke="none" d="M10 6h12v10H10z" />
          <path fill="none" stroke="#163f37" d="M12 9v2m7-2v2m-5 2h4l1-1" />
          <path stroke="#fff" d="M7 3h17M5 28h22" />
          <path stroke="#999" d="M8 19h11M24 19h1M7 29h18" />
        </g>
      ) : name === 'welcome-door' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M2 28h28v3H2z" />
          <path fill="#c7c7bc" d="M5.5 27.5v-24h21v24z" />
          <path fill="#eee3bb" d="M8.5 27.5v-21h15v21z" />
          <path fill="#fff5d7" stroke="none" d="M9 7h5v20H9zM9 26h6l-3 4H3z" />
          <path fill="#6c8c83" d="m15.5 8.5 8-2v21l-8 3z" />
          <path fill="#496961" stroke="none" d="M22 9h1v18l-7 3v-2l6-2z" />
          <path stroke="#aeccc0" fill="none" d="M17 10l4-1M17.5 11v14" />
          <path fill="#ead9a0" stroke="none" d="M17 18h2v2h-2z" />
          <path stroke="#f0f0e8" fill="none" d="M6.5 25V4.5h18" />
          <path stroke="#666" fill="none" d="M28 5v22M23 29h6" />
        </g>
      ) : name === 'appearance' ? (
        <g {...common}>
          <path fill="#bbb" d="M1.5 4.5h29v23h-29z" />
          <path fill="#eee" d="M2.5 5.5h27v21h-27z" />
          <path fill="#647d73" d="M4.5 7.5h23v16h-23z" />
          <path stroke="none" fill="#a6b8ac" d="M5 8h22v8H5z" />
          <path stroke="none" fill="#e7e1a9" d="M22 9h3v3h-3z" />
          <path stroke="none" fill="#879d8c" d="M5 17l6-6 8 10h8v2H5z" />
          <path stroke="none" fill="#4b6554" d="M15 23l7-8 5 4v4z" />
          <path stroke="#fff" d="M3 6h25M3 7v18" />
          <path stroke="#999" d="M4 25h23" />
        </g>
      ) : name === 'applications' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="m9 27 7 3 11-4 2 1-13 5-8-4z" />
          <path fill="#bbab84" d="m4.5 15.5 11.5 5v10l-11.5-5z" />
          <path fill="#9b8b66" d="m16 20.5 11.5-5v10l-11.5 5z" />
          <path fill="#615642" d="m4.5 15.5 11.5-5 11.5 5-11.5 5z" />
          <path fill="#cbbd98" d="m4.5 15.5-3-4 11-5 3.5 4z" />
          <path fill="#b6a780" d="m16 10.5 3.5-4 11 5-3 4z" />
          <path fill="#759087" d="m10.5 2.5 12 2.5v12.5L16 20.5l-5.5-2.5z" />
          <path fill="#416258" stroke="none" d="m12 4 9 2v3l-9-2z" />
          <path fill="none" stroke="#e5eee8" d="M12 8.5v5m2-3 5 1m-5 2 4 1" />
          <path fill="#e1d3af" d="m4.5 15.5 11.5 5-3.5 4-11-5z" />
          <path fill="#d3c39e" d="m16 20.5 11.5-5 3 4-11 5z" />
          <path fill="none" stroke="#f9eed2" d="m4 17 9.5 4M18.5 21l9-4" />
          <path fill="none" stroke="#736344" d="m6 25 8.5 3.5m3-.5 8.5-3.5" />
        </g>
      ) : name === 'checklist' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M6 5h23v26H6z" />
          <path fill="#b7b29f" d="M4.5 3.5h23v27h-23z" />
          <path fill="none" stroke="#e8e5d8" d="M5 4h21M5 5v24" />
          <path fill="#f6f4e9" d="M7.5 6.5h17v21h-17z" />
          <path fill="none" stroke="#fff" d="M8 7h15M8 8v18" />
          <path fill="#bbb" d="M11.5 1.5h9v6h-9z" />
          <path fill="none" stroke="#eee" d="M12 2h7M12 3v3" />
          <path fill="none" stroke="#777" d="M14 4h4" />
          <path
            fill="none"
            stroke="#315e45"
            strokeWidth="2"
            d="m9 12 2 2 4-4m-6 9 2 2 4-4"
          />
          <path fill="none" stroke="#777" d="M9.5 23.5h4v3h-4z" />
          <path fill="none" stroke="#777" d="M17 12.5h5M17 19.5h5M17 25.5h5" />
        </g>
      ) : name === 'code-review' ? (
        <g {...common}>
          <path
            fill="#24292f"
            d="M10.5 1.5h9v1h3v1h2v2h2v2h1v3h1v9h-1v3h-1v2h-2v2h-2v1h-3v1h-9v-1h-3v-1h-2v-2h-2v-2h-1v-3h-1v-9h1v-3h1v-2h2v-2h2v-1h3z"
          />
          <path fill="none" stroke="#69737e" d="M11 3h8M8 4h2M6 6h2M3.5 11v7" />
          <path
            fill="#f6f8fa"
            stroke="none"
            d="M9 6h2v1h2v1h5V7h2V6h2v2h1v4h1v4h-1v2h-2v1h-3v2h1v5h-7v-3H9v-1H7v-2H6v-1H4v-2h2v1h2v2h2v1h2v-2h-2v-1H8v-2H7v-4h1V8h1z"
          />
          <path fill="#73d991" d="M23.5 20.5h5l3 3v5l-3 3h-5l-3-3v-5z" />
          <path fill="none" stroke="#c7f6d4" d="M24 22h4M22 24v4" />
          <path
            fill="none"
            stroke="#143b28"
            strokeWidth="2"
            d="m23 26 2 2 4-4"
          />
        </g>
      ) : name === 'gallery' ? (
        <g {...common}>
          <path fill="#ccc" d="M2.5 2.5h27v27h-27z" />
          <path stroke="#fff" d="M3 3h25M3 4v24" />
          <path fill="#d8e3d7" d="M5.5 6.5h9v7h-9z" />
          <path fill="#5e7563" d="M18.5 6.5h7v7h-7z" />
          <path fill="#eee" d="M5.5 18.5h9v7h-9z" />
          <path fill="#9baf9c" d="M18.5 18.5h7v7h-7z" />
          <path stroke="#fff" d="M6 7h7M19 7h5M6 19h7M19 19h5" />
          <path stroke="#163f37" d="M7 22h5M20 21l1 2 3-3" />
        </g>
      ) : name === 'virtual-machines' ? (
        <g {...common}>
          <path fill="#888" d="M8.5 2.5h21v22h-21z" />
          <path fill="#ddd" d="M8.5 2.5h21v6h-21z" />
          <path stroke="#fff" d="M10 4h18M10 5v2" />
          <path fill="#bcbcbc" d="M2.5 8.5h21v22h-21z" />
          <path
            fill="#eee"
            d="M3.5 9.5h19v6h-19zM3.5 17.5h19v5h-19zM3.5 24.5h19v5h-19z"
          />
          <path stroke="#777" d="M6 12h8M6 20h8M6 27h8" />
          <path
            fill="#6d8f7c"
            d="M17.5 11.5h3v2h-3zM17.5 19.5h3v2h-3zM17.5 26.5h3v2h-3z"
          />
          <path fill="none" stroke="#555" d="M26 12h2v9h-2" />
        </g>
      ) : name === 'virtual-machine' ? (
        <g {...common}>
          <path fill="#888" d="M7.5 3.5h19l4 4v22h-23z" />
          <path fill="#ccc" d="M2.5 7.5h24v22h-24z" />
          <path fill="#eee" stroke="none" d="M3 8h23v2H3zM3 10h2v18H3z" />
          <path fill="#647f73" d="M6.5 11.5h16v10h-16z" />
          <path fill="#b8cdc1" stroke="none" d="M8 13h12v2H8z" />
          <path fill="none" stroke="#dbe8df" d="M9 17h3v2H9zM16 17h4" />
          <path fill="#6d8f7c" d="M20.5 25.5h2v2h-2z" />
          <path fill="none" stroke="#777" d="M7 26h9M27 10v17" />
          <path fill="none" stroke="#eee" d="M9 5h16l2 2" />
        </g>
      ) : name === 'new-virtual-machine' ? (
        <g {...common}>
          <path fill="#898b8a" d="M6.5 3.5h15l4 4v22h-19z" />
          <path fill="#c8cac8" d="M2.5 7.5h19v22h-19z" />
          <path stroke="#f1f2ee" fill="none" d="M4 27V9h16M8 5h13l2 2" />
          <path fill="#59756b" d="M6.5 12.5h11v8h-11z" />
          <path stroke="#c1d6ca" d="M8 14h8M8 17h5" />
          <path stroke="#727b76" d="M7 25h7" />
          <path fill="#a9c8ac" d="M20.5 16.5h5v4h5v5h-5v5h-5v-5h-5v-5h5z" />
          <path stroke="#e5f0df" fill="none" d="M22 20v-2h2M17 22h4" />
          <path stroke="#5f8768" fill="none" d="M26 24h3M24 25v4h-2" />
        </g>
      ) : name === 'virtual-machine-inspector' ? (
        <g {...common}>
          <path fill="#767d7c" d="M6.5 2.5h16l4 4v20h-20z" />
          <path fill="#c0c8c5" d="M2.5 6.5h20v20h-20z" />
          <path stroke="#ebf0eb" fill="none" d="M4 25V8h17M8 4h14l2 2" />
          <path fill="#59756b" d="M6.5 11.5h12v8h-12z" />
          <path stroke="#c1d6ca" d="M8 13h9M8 16h5" />
          <path stroke="#747e79" d="M7 23h7" />
          <path fill="#b09360" d="m24.5 21.5 6 6-3 3-6-6z" />
          <path stroke="#f0daa6" d="m25 24 4 4" />
          <path
            fill="#d7bd83"
            d="M17.5 13.5h7v2h2v2h2v7h-2v2h-2v2h-7v-2h-2v-2h-2v-7h2v-2h2z"
          />
          <path fill="#d3e2dc" d="M18.5 16.5h5v2h2v5h-2v2h-5v-2h-2v-5h2z" />
          <path stroke="#f9faf2" fill="none" d="M18 21v-2h2v-1h2" />
        </g>
      ) : name === 'network' ? (
        <g {...common}>
          <path fill="none" d="M15.5 12v6h-10v4m10-4h11v4" />
          <path stroke="#fff" fill="none" d="M16.5 13v6h-10v2m10-2h11v2" />
          <path
            fill="#ddd"
            d="M9.5 1.5h13v10h-13zM1.5 21.5h10v9h-10zM21.5 21.5h9v9h-9z"
          />
          <path
            fill="#91a59b"
            d="M11.5 3.5h9v5h-9zM3.5 23.5h6v4h-6zM23.5 23.5h5v4h-5z"
          />
          <path stroke="#fff" d="M10 2h11M2 22h8M22 22h7" />
          <path stroke="#888" d="M12 10h5M3 29h4M23 29h3" />
        </g>
      ) : name === 'cloud-tunnel' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M5 22h24v2H5z" />
          <path
            fill="#dce5e3"
            d="M5.5 21.5h-3v-9h2v-3h5v-4h3v-2h7v2h3v4h5v3h2v7h-2v2z"
          />
          <path
            fill="none"
            stroke="#fff"
            d="M4 19v-5h2v-3h5V7h3V5h5M24 11h2v3h2"
          />
          <path fill="none" stroke="#a0b4b0" d="M7 20h3m12 0h4v-3" />
          <path fill="#c8a46e" d="M11.5 21.5v-7h2v-2h5v2h2v7z" />
          <path fill="none" stroke="#f5dfb3" d="M13 20v-5h2v-1h3" />
          <path fill="#38584d" d="M14.5 21.5v-5h3v5" />
          <path fill="none" stroke="#262626" strokeWidth="3" d="M16 22v5" />
          <path fill="none" stroke="#a7c4b8" d="M16 22v5" />
          <path fill="#bcbcbc" d="M8.5 25.5h15v5h-15z" />
          <path fill="none" stroke="#fff" d="M9 26h13M9 27v2" />
          <path fill="none" stroke="#777" d="M10 30h13v-3" />
          <path fill="#52775f" stroke="none" d="M19 27h2v2h-2z" />
          <path fill="none" stroke="#777" d="M11 28.5h5" />
        </g>
      ) : name === 'mesh-network' ? (
        <g {...common} shapeRendering="geometricPrecision">
          {[6, 16, 26].flatMap((y) =>
            [6, 16, 26].map((x) => (
              <g key={`${x}-${y}`} transform={`translate(${x} ${y})`}>
                <circle r="3.5" fill="#aaa" />
                <path fill="none" stroke="#eee" d="M-2 0a2 2 0 0 1 2-2" />
                <path fill="none" stroke="#666" d="M2 0a2 2 0 0 1-2 2" />
              </g>
            )),
          )}
        </g>
      ) : name === 'save' ? (
        <g {...common}>
          <path fill="#555f5c" d="M3.5 1.5h21l4 4v25h-25z" />
          <path stroke="#aabbb5" fill="none" d="M4 2h20M4 3v26" />
          <path fill="#bbb" d="M8.5 1.5h14v12h-14z" />
          <path fill="#555" d="M17.5 3.5h3v7h-3z" />
          <path fill="#fafafa" d="M8.5 18.5h15v12h-15z" />
          <path fill="#ccf" stroke="none" d="M9 19h14v3H9z" />
          <path stroke="#aaa" d="M11 24h10M11 27h8" />
          <path stroke="#fff" d="M10 3h5M10 4v7" />
        </g>
      ) : name === 'info' ? (
        <g {...common}>
          <path fill="#555f5c" d="M8.5 1.5h16l6 6v17l-6 6h-16l-7-6v-17z" />
          <path fill="#d6e3dd" d="M9.5 2.5h14l6 6v15l-6 6h-14l-7-6v-15z" />
          <path stroke="#fff" fill="none" d="M9 4h14M4 9v13" />
          <path
            fill="#163f37"
            stroke="none"
            d="M14 7h5v4h-5zM12 14h7v10h3v2H11v-2h3v-8h-2z"
          />
        </g>
      ) : name === 'sliders' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M5 6h25v24H5z" />
          <path fill="#bcbfba" d="M2.5 3.5h26v25h-26z" />
          <path fill="none" stroke="#eee" d="M4 5h23M4 6v21" />
          <path fill="#e0e4dc" d="M6.5 7.5h18v17h-18z" />
          <path fill="none" stroke="#a5ada3" d="M8 13h15M8 19h15" />
          <path fill="none" stroke="#454e48" d="M10.5 9v14m5-14v14m5-14v14" />
          <path fill="#7f9d8e" d="M8.5 11.5h4v3h-4zM13.5 17.5h4v3h-4z" />
          <path fill="#b9ac7c" d="M18.5 13.5h4v3h-4z" />
          <path stroke="#e7eee8" d="M9 12h3M14 18h3M19 14h3" />
          <path fill="none" stroke="#717a74" d="M5 27h22V6" />
        </g>
      ) : name === 'document-return' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M6 4h19v26H6z" />
          <path fill="#f0e9d3" d="M3.5 2.5h15l6 6v20h-21z" />
          <path fill="#c9c5ad" d="M18.5 2.5v6h6" />
          <path fill="none" stroke="#fff8e9" d="M5 4h12M5 5v22" />
          <path fill="none" stroke="#8f9282" d="M7 10h10M7 14h12M7 18h7" />
          <path fill="#6f9b87" d="m12.5 22.5 8-6v3h5v-5h4v6l-3 3h-6v5z" />
          <path fill="none" stroke="#c2ded0" d="m15 22 4-3m3 1h4v-4" />
          <path fill="none" stroke="#3a6153" d="M22 22h4l2-2" />
        </g>
      ) : name === 'notification-bell' ? (
        <g {...common}>
          <path fill="#777" stroke="none" d="M10 29h12v1H10z" />
          <path fill="#c3a469" d="M13.5 3.5h5v5h-5z" />
          <path fill="#976f3f" d="M13.5 22.5h5v4l-1 2h-3l-1-2z" />
          <path
            fill="#c9ab6c"
            d="M12.5 6.5h7v2h3v3h2v8l3 3v3h-23v-3l3-3v-8h2v-3h3z"
          />
          <path
            fill="#f1dfae"
            stroke="none"
            d="M13 7h6v2h-6v2h-3v3H8v-2h2V9h3zM8 19h2v2l-2 2H5z"
          />
          <path fill="#977946" stroke="none" d="M22 11h2v8l3 3v3H5v-2h17z" />
          <path stroke="#f3e6bf" fill="none" d="M7 23.5h17" />
          <path stroke="#5e472e" fill="none" d="M6 25.5h20" />
        </g>
      ) : (
        <g {...common}>
          <path fill="#aaa" d="M6.5 2.5h14l6 6v23h-20z" />
          <path
            fill={name === 'notes' ? '#fffbd7' : '#fff'}
            d="M5.5 1.5h14l6 6v23h-20z"
          />
          <path fill="#ddd" d="M19.5 1.5v6h6" />
          <path
            stroke={name === 'notes' ? '#eeebc9' : '#eee'}
            d="M7 3v25M8 29h15"
          />
          {name === 'image' ? (
            <>
              <path fill="#adc3b6" d="M8.5 11.5h14v13h-14z" />
              <path stroke="none" fill="#eee7bc" d="M18 13h3v3h-3z" />
              <path stroke="none" fill="#597463" d="M9 22l4-6 5 6 4-4v6H9z" />
              <path stroke="#aaa" d="M9 27h11" />
            </>
          ) : (
            <>
              <path
                stroke={name === 'notes' ? '#aaa882' : '#999'}
                d="M9 11h12M9 14h12M9 17h12M9 20h12M9 23h8"
              />
              {name === 'notes' && (
                <>
                  <path
                    fill="#d2af5c"
                    d="M23.5 13.5l3-1 3 3-1 3-10 10-4 1 1-4z"
                  />
                  <path stroke="#fff0af" d="M16 26l10-10" />
                  <path fill="#555" d="M14.5 29.5l1-4 3 3z" />
                </>
              )}
            </>
          )}
        </g>
      )}
    </svg>
  );
}

export function Button({
  primary = false,
  className = '',
  children,
  onPointerDown,
  onClick,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { primary?: boolean }) {
  return (
    <button
      type="button"
      className={`ns-button ${primary ? 'ns-default' : ''} ${className}`}
      {...props}
      onPointerDown={(event) => {
        // Safari does not focus buttons on pointer activation. Modal openers must
        // have a stable focus target before their click handler opens the dialog.
        if (event.button === 0)
          event.currentTarget.focus({ preventScroll: true });
        onPointerDown?.(event);
      }}
      onClick={(event) => {
        // WebKit's intervening native mousedown can undo pointerdown focus.
        // Reaffirm it at activation, before application code opens a modal/menu.
        event.currentTarget.focus({ preventScroll: true });
        onClick?.(event);
      }}
    >
      {children}
    </button>
  );
}

type ButtonIcon =
  | 'more'
  | 'attachment'
  | 'copy'
  | 'paste'
  | 'refresh'
  | 'back'
  | 'up'
  | 'sort-ascending'
  | 'sort-descending'
  | 'page-up'
  | 'page-down';

/** Original monochrome drawings shared by window chrome and small controls. */
export function WindowControlIcon({
  icon,
}: {
  icon:
    | 'minimize'
    | 'menu'
    | 'shade'
    | 'unshade'
    | 'maximize'
    | 'restore'
    | 'overview'
    | 'close';
}) {
  return (
    <svg
      className="ns-control-icon"
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinejoin="miter"
      aria-hidden="true"
      focusable="false"
    >
      {icon === 'minimize' ? (
        <>
          <path d="M3 3h10v10H3z" />
          <path d="M5 9h6v2H5z" fill="currentColor" stroke="none" />
        </>
      ) : icon === 'menu' || icon === 'unshade' ? (
        <path d="m4 6 4 4 4-4" />
      ) : icon === 'shade' ? (
        <path d="m4 10 4-4 4 4" />
      ) : icon === 'maximize' ? (
        <path d="M3 3h10v10H3zM3 5h10" />
      ) : icon === 'restore' ? (
        <path d="M6 6h7v7H6zM3 10V3h7v3" />
      ) : icon === 'overview' ? (
        <path
          fill="currentColor"
          fillRule="evenodd"
          stroke="none"
          shapeRendering="crispEdges"
          d="M1 1h10v4H9V3H3v6h2v2H1zM5 5h10v10H5zM7 7h6v1H7zM7 9h6v4H7z"
        />
      ) : (
        <path d="m4 4 8 8M12 4l-8 8" />
      )}
    </svg>
  );
}

export function IconButton({
  icon,
  label,
  className = '',
  title = label,
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children' | 'aria-label'> & {
  icon: ButtonIcon;
  label: string;
}) {
  return (
    <Button
      {...props}
      className={`ns-icon-button ${className}`}
      aria-label={label}
      title={title}
    >
      <svg
        width="16"
        height="16"
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="miter"
        aria-hidden="true"
        focusable="false"
      >
        {icon === 'more' ? (
          <path
            fill="currentColor"
            stroke="none"
            d="M2 7h2v2H2zM7 7h2v2H7zM12 7h2v2h-2z"
          />
        ) : icon === 'attachment' ? (
          <path d="m6 6 4-4a2.8 2.8 0 0 1 4 4l-7 7a3.5 3.5 0 0 1-5-5l7-7m-4 8 5-5a.7.7 0 0 1 1 1l-5 5" />
        ) : icon === 'copy' ? (
          <>
            <path d="M4 11H2V2h8v2" />
            <path d="M6 6h8v8H6z" />
          </>
        ) : icon === 'paste' ? (
          <>
            <path d="M5 3H3v11h10V3h-2M5 7h6M5 10h6" />
            <path d="M5 2h6v3H5z" />
          </>
        ) : icon === 'refresh' ? (
          <path d="M13 6a5 5 0 1 0 0 5M13 2v4H9" />
        ) : icon === 'back' ? (
          <path d="m7 3-5 5 5 5M2 8h12" />
        ) : icon === 'up' ? (
          <path d="m3 7 5-5 5 5M8 2v12" />
        ) : icon === 'sort-ascending' ? (
          <path d="M1 3h3M1 8h5M1 13h7M12 14V2m-3 3 3-3 3 3" />
        ) : icon === 'sort-descending' ? (
          <path d="M1 3h7M1 8h5M1 13h3M12 2v12m-3-3 3 3 3-3" />
        ) : icon === 'page-up' ? (
          <path d="M2 2h12M4 9l4-4 4 4M8 5v9" />
        ) : (
          <path d="M2 14h12M4 7l4 4 4-4M8 2v9" />
        )}
      </svg>
    </Button>
  );
}

export function Field({
  label,
  className = '',
  id,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { label?: ReactNode }) {
  const generated = useId();
  const inputId = id || generated;
  const input = (
    <input id={inputId} className={`ns-field ${className}`} {...props} />
  );
  return label ? (
    <label className="ns-field-label" htmlFor={inputId}>
      {label}
      {input}
    </label>
  ) : (
    input
  );
}

export function Select({
  label,
  className = '',
  children,
  id,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & { label?: ReactNode }) {
  const generated = useId();
  const selectId = id || generated;
  const select = (
    <span className={`ns-select ${className}`}>
      <select id={selectId} {...props}>
        {children}
      </select>
      <span aria-hidden="true" className="ns-select-arrows">
        <svg viewBox="0 0 8 14" width="8" height="14" focusable="false">
          <path d="M1 5 4 2 7 5ZM1 9 4 12 7 9Z" />
        </svg>
      </span>
    </span>
  );
  return label ? (
    <label className="ns-field-label" htmlFor={selectId}>
      {label}
      {select}
    </label>
  ) : (
    select
  );
}

export function Checkbox({
  children,
  label,
  className = '',
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  label?: ReactNode;
}) {
  return (
    <label className={`ns-checkbox ${className}`}>
      <input type="checkbox" {...props} />
      <span>{label ?? children}</span>
    </label>
  );
}

export function ButtonGroup({
  children,
  label,
  className = '',
}: {
  children: ReactNode;
  label: string;
  className?: string;
}) {
  return (
    <div
      role="group"
      aria-label={label}
      className={`ns-button-group ${className}`}
    >
      {children}
    </div>
  );
}

export function Tabs({
  tabs,
  value,
  onChange,
  children,
  label = 'Tabs',
}: {
  tabs: {
    id: string;
    label: string;
    content?: ReactNode;
    disabled?: boolean;
  }[];
  value: string;
  onChange: (id: string) => void;
  children?: ReactNode;
  label?: string;
}) {
  const prefix = useId();
  return (
    <div className="ns-tabs">
      <div
        role="tablist"
        aria-label={label}
        className="ns-tab-list"
        onKeyDown={(event) => {
          if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key))
            return;
          event.preventDefault();
          const available = tabs.filter((tab) => !tab.disabled);
          const current = available.findIndex((tab) => tab.id === value);
          const index =
            event.key === 'Home'
              ? 0
              : event.key === 'End'
                ? available.length - 1
                : (current +
                    (event.key === 'ArrowRight' ? 1 : -1) +
                    available.length) %
                  available.length;
          const next = available[index];
          if (next) {
            onChange(next.id);
            document.getElementById(`${prefix}-${next.id}`)?.focus();
          }
        }}
      >
        {tabs.map((tab) => (
          <button
            type="button"
            id={`${prefix}-${tab.id}`}
            key={tab.id}
            role="tab"
            disabled={tab.disabled}
            aria-selected={tab.id === value}
            aria-controls={`${prefix}-panel`}
            tabIndex={tab.id === value ? 0 : -1}
            onClick={(event) => {
              event.currentTarget.focus({ preventScroll: true });
              onChange(tab.id);
            }}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div
        id={`${prefix}-panel`}
        role="tabpanel"
        aria-labelledby={`${prefix}-${value}`}
        className="ns-tab-panel"
      >
        {tabs.find((tab) => tab.id === value)?.content ?? children}
      </div>
    </div>
  );
}

export function StatusBar({
  children,
  className = '',
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div role="status" className={`ns-status ${className}`}>
      {children}
    </div>
  );
}

export function Radio({
  label,
  children,
  className = '',
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'> & {
  label?: ReactNode;
}) {
  return (
    <label className={`ns-radio ${className}`}>
      <input type="radio" {...props} />
      <span>{label ?? children}</span>
    </label>
  );
}

export function TextArea({
  label,
  id,
  className = '',
  ...props
}: ComponentPropsWithRef<'textarea'> & { label?: ReactNode }) {
  const generated = useId();
  const inputId = id ?? generated;
  const input = (
    <textarea id={inputId} className={`ns-textarea ${className}`} {...props} />
  );
  return label ? (
    <label className="ns-field-label" htmlFor={inputId}>
      {label}
      {input}
    </label>
  ) : (
    input
  );
}
