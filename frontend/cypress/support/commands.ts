declare global {
  namespace Cypress {
    interface Chainable {
      login(): Chainable<void>;
    }
  }
}

Cypress.Commands.add('login', () => {
  cy.window().then((win) => {
    win.sessionStorage.setItem('console.devMode', 'true');
  });
});

export {};
