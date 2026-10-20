(defun page (title content)
  "Compose a text buffer; only explicit text nodes add visible whitespace."
  (ui:document title (ui:main (ui:h1 title) (ui:text "\n") content)))

(defmacro defgroup (name help)
  "Declare a named option group during revision preparation."
  (quasiquote (site:group (quote (unquote name)) (unquote help))))

(defmacro defcustom (name default help &key type group)
  "Declare a bounded data option; this is a website API, not Emacs Lisp."
  (quasiquote (site:custom (quote (unquote name)) (unquote default)
    (unquote help) (unquote type) (unquote group))))
