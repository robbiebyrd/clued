// Every form of the using directive, and a file-scoped namespace.
global using System.Linq;
using System;
using System.IO;
using static System.Math;
using static System.Console;
using Json = System.Text.Json.JsonSerializer;
using IntList = System.Collections.Generic.List<int>;
using global::System.Threading.Tasks;

namespace Demo.Tools.Io;

/// <summary>Reads lines from a file.</summary>
public static class LineReader
{
    public static async Task<string[]> ReadAsync(string path)
    {
        using var reader = new StreamReader(path);
        var text = await reader.ReadToEndAsync();
        WriteLine(Sqrt(text.Length));
        return text.Split('\n').Where(l => l.Length > 0).ToArray();
    }

    public static string Dump(object value) => Json.Serialize(value);

    public static IntList Lengths(string[] lines) => new IntList(lines.Select(l => l.Length));
}
