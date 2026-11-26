// Página de retorno do login (popup) do Coruja Backup. Não guarda nada: entrega o código à janela do Coruja que abriu este
// popup, e só a ela (a origem vem no "state" e é conferida), usando postMessage. Nada é enviado a servidor algum.
(function () {
  var t = document.getElementById('t'), m = document.getElementById('m');
  function fail(msg) { t.textContent = 'Não foi possível concluir'; m.textContent = msg; }
  var q = new URLSearchParams(location.search);
  var state = q.get('state') || '', code = q.get('code') || '', err = q.get('error') || '';
  var i = state.indexOf('.'), origin = '';
  try { origin = atob(state.slice(i + 1).replace(/-/g, '+').replace(/_/g, '/')); } catch (e) { origin = ''; }
  history.replaceState(null, '', location.pathname); // tira o código da barra de endereço
  if (i < 1 || !/^https?:\/\/(\[[0-9a-fA-F:]+\]|[A-Za-z0-9.\-]+)(:[0-9]{1,5})?$/.test(origin)) { fail('Resposta de login inválida. Feche esta janela e tente de novo no Coruja.'); return; }
  if (!window.opener) { fail('Esta janela deve ser aberta pelo Coruja. Feche-a e clique em “Entrar com o Google” no Coruja.'); return; }
  if (!code && !err) { fail('O provedor não devolveu o código. Feche esta janela e tente de novo.'); return; }
  try { window.opener.postMessage({ type: 'coruja-oauth', state: state, code: code, error: err }, origin); } catch (e) { fail('Não foi possível falar com o Coruja.'); return; }
  t.textContent = err ? 'Acesso não autorizado' : 'Conta autorizada';
  m.textContent = err ? 'Você pode fechar esta janela.' : 'Você já pode fechar esta janela e voltar ao Coruja.';
  setTimeout(function () { window.close(); }, 600);
})();
