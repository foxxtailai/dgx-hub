param(
    [Parameter(Mandatory = $true)]
    [string]$Executable,
    [switch]$RebuildCache
)
$ErrorActionPreference = 'Stop'
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { return }
$executablePath = (Resolve-Path -LiteralPath $Executable -ErrorAction Stop).Path
if (-not ('DGXHubBuildShell' -as [type])) {
    Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class DGXHubBuildShell {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct FileInfo {
        public IntPtr icon;
        public int index;
        public uint attributes;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)] public string name;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 80)] public string type;
    }
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr SHGetFileInfo(string path, uint attributes, out FileInfo info, uint size, uint flags);
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    private static extern void SHUpdateImage(string path, int index, uint flags, int imageIndex);
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    public static extern void SHChangeNotify(int eventId, uint flags, string item, IntPtr unused);
    public static void RefreshIcon(string path) {
        FileInfo cached, location;
        uint size = (uint)Marshal.SizeOf(typeof(FileInfo));
        // Resolve the existing file-icon slot and its resource location before
        // invalidating it. Updating the item alone can retain Explorer's image.
        if (SHGetFileInfo(path, 0, out cached, size, 0x4000) != IntPtr.Zero &&
            SHGetFileInfo(path, 0, out location, size, 0x1000) != IntPtr.Zero &&
            !String.IsNullOrEmpty(location.name)) {
            SHUpdateImage(location.name, location.index, 0, cached.index);
        }
        SHChangeNotify(0x2000, 0x1005, path, IntPtr.Zero);
    }
}
'@
}
# Notify only this replaced file; Explorer can otherwise retain its old icon.
# SHCNE_UPDATEITEM, SHCNF_PATHW | SHCNF_FLUSH.
[DGXHubBuildShell]::RefreshIcon($executablePath)
if ($RebuildCache) {
    # Ask the Shell to invalidate persistent icon caches. This is an explicit
    # repair option for old icons that survive per-file updates, not a build step.
    [DGXHubBuildShell]::SHChangeNotify(0x08000000, 0x1000, $null, [IntPtr]::Zero)
}
