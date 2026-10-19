// A file with a syntax error: what parses around it is kept.
using System;

namespace Demo.Broken
{
    public class Good
    {
        public int Value() => 1;
    }

    public class Bad
    {
        public void Half(int x
        {
            Console.WriteLine(x;
        }

        public string After() => "after";
    }

    public enum Mode { On, Off }
}
